package echo

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	echolib "github.com/labstack/echo/v4"
	"github.com/rennf93/guard-core-go/v4/guardcore"
)

const failClosedMessage = "Security check failed"

type middleware struct {
	engine   *guardcore.Engine
	maxBytes int64
	logger   *log.Logger
}

type Option func(*middleware)

func WithMaxBodyBytes(maxBodyBytes int64) Option {
	return func(m *middleware) {
		if maxBodyBytes > 0 {
			m.maxBytes = maxBodyBytes
		}
	}
}

func WithLogger(logger *log.Logger) Option {
	return func(m *middleware) {
		if logger != nil {
			m.logger = logger
		}
	}
}

func New(engine *guardcore.Engine, opts ...Option) (echolib.MiddlewareFunc, error) {
	if engine == nil {
		return nil, errors.New("engine must not be nil")
	}
	m := &middleware{engine: engine, maxBytes: DefaultMaxBodyBytes, logger: log.Default()}
	for _, opt := range opts {
		opt(m)
	}
	return m.wrap, nil
}

func (m *middleware) wrap(next echolib.HandlerFunc) echolib.HandlerFunc {
	return func(c echolib.Context) error {
		req := newRequestShim(c, m.maxBytes)
		verdict, err := m.check(req)
		if err != nil {
			m.logger.Printf("guardcore echo: engine malfunction, failing closed: %v", err)
			applyResponse(c, m.engine.CreateErrorResponse(500, failClosedMessage))
			return nil
		}
		if verdict != nil {
			applyResponse(c, verdict)
			return nil
		}
		// Security headers on the pass-through path: the engine computes the
		// set (blocked verdicts already carry it), the adapter applies it
		// before the handler writes its response. CORS response headers are
		// merged on top (the reference _inject_cors_headers runs after the
		// security-header set, so CORS wins on a shared name) whenever the
		// request carries an Origin and CORS is enabled.
		cors := m.engine.CORSResponseHeaders(req)
		headers := m.engine.ResponseHeaders()
		for name, value := range cors {
			headers[name] = value
		}
		for name, value := range headers {
			c.Response().Header().Set(name, value)
		}
		// Behavioral return rules (route and global) run against the
		// response the handler produced, exactly like the reference
		// response factory's behavioral phase. The adapter captures the
		// leading response body up to the configured inspect budget and
		// hands status code plus captured prefix to the engine after the
		// handler ran: return rules never modify the response. With a
		// CustomResponseModifier configured the whole response is held
		// instead, so the modifier (the last step of the reference
		// process_response) can rewrite it before it is sent.
		origWriter := c.Response().Writer
		var held *heldWriter
		if m.engine.Config.CustomResponseModifier != nil {
			held = &heldWriter{ResponseWriter: origWriter}
			c.Response().Writer = held
		}
		capture := m.newBodyCapture()
		if capture != nil && held == nil {
			c.Response().Writer = capture.wrap(origWriter)
		}
		err = next(c)
		// A handler error is turned into a response by Echo's
		// HTTPErrorHandler, which normally runs after the whole middleware
		// chain returned: c.Response().Status would still be the unwritten
		// default and the engine would record a 200. Run the error handler
		// here, as Echo's own logger middleware does, so the real status
		// (and the held body, when a modifier is configured) exists first.
		if err != nil {
			c.Error(err)
		}
		var observedBody []byte
		if capture != nil {
			observedBody = capture.body()
		}
		if held != nil {
			if held.hijacked {
				return nil
			}
			if capture != nil {
				observedBody = held.body
				if len(observedBody) > capture.budget {
					observedBody = observedBody[:capture.budget]
				}
			}
			c.Response().Writer = origWriter
		}
		m.engine.ProcessResponse(req, &guardcore.Response{
			StatusCode: c.Response().Status,
			Headers:    map[string]string{},
			Body:       observedBody,
		})
		if held != nil {
			held.release(m.engine, c.Response().Status, cors)
		}
		return nil
	}
}

// bodyCapture records the leading bytes of the pass-through response body,
// bounded by the engine's behavior_max_response_body_inspect_bytes budget,
// and only when behavior_scan_response_body is enabled (with the flag off
// the engine rejects every rule that would need the body, so there is
// nothing to inspect).
type bodyCapture struct {
	budget int
	buf    []byte
}

func (m *middleware) newBodyCapture() *bodyCapture {
	if !m.engine.Config.BehaviorScanResponseBody {
		return nil
	}
	budget := m.engine.Config.BehaviorMaxResponseBodyInspectBytes
	if budget <= 0 {
		return nil
	}
	return &bodyCapture{budget: budget}
}

func (c *bodyCapture) wrap(w http.ResponseWriter) http.ResponseWriter {
	return &captureWriter{ResponseWriter: w, capture: c}
}

func (c *bodyCapture) body() []byte { return c.buf }

type captureWriter struct {
	http.ResponseWriter
	capture *bodyCapture
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if remaining := w.capture.budget - len(w.capture.buf); remaining > 0 {
		if len(p) < remaining {
			w.capture.buf = append(w.capture.buf, p...)
		} else {
			w.capture.buf = append(w.capture.buf, p[:remaining]...)
		}
	}
	return w.ResponseWriter.Write(p)
}

func (m *middleware) check(req guardcore.Request) (verdict *guardcore.Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("engine panic: %v", r)
		}
	}()
	return m.engine.Check(req), nil
}

func applyResponse(c echolib.Context, response *guardcore.Response) {
	for name, value := range response.Headers {
		c.Response().Header().Set(name, value)
	}
	c.Response().WriteHeader(response.StatusCode)
	if len(response.Body) > 0 {
		_, _ = c.Response().Write(response.Body)
	}
	// Aborting in Echo means not calling next: the wrap caller returns nil here.
}
