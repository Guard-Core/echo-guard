package echo

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	echolib "github.com/labstack/echo/v5"
	"github.com/rennf93/guard-core-go/v4/guardcore"
)

func TestParityHeldWriterHoldsAndFlushIsHarmless(t *testing.T) {
	rec := httptest.NewRecorder()
	held := &heldWriter{ResponseWriter: rec}
	held.WriteHeader(http.StatusTeapot)
	if _, err := held.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if err := held.FlushError(); err != nil {
		t.Fatal(err)
	}
	if string(held.body) != "ab" || rec.Body.Len() != 0 || rec.Code != http.StatusOK {
		t.Fatalf("nothing may reach the client before release: %q %d %d", held.body, rec.Body.Len(), rec.Code)
	}
}

func TestParityFlushInsideHeldResponseDoesNotPanic(t *testing.T) {
	engine := newTestEngine(t, func(c *guardcore.SecurityConfig) {
		c.CustomResponseModifier = func(resp *guardcore.Response) *guardcore.Response { return resp }
	})
	rec := serveWith(t, engine, func(c *echolib.Context) error {
		_, _ = c.Response().Write([]byte("part"))
		_ = http.NewResponseController(c.Response()).Flush()
		return nil
	}, httptest.NewRequest("GET", "/stream", nil))
	if rec.Code != 200 || rec.Body.String() != "part" {
		t.Fatalf("flush in a held response must be harmless, got %d %q", rec.Code, rec.Body.String())
	}
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
}

func (hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, client := net.Pipe()
	_ = client.Close()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func TestParityHijackedResponseIsNotReleased(t *testing.T) {
	engine := newTestEngine(t, func(c *guardcore.SecurityConfig) {
		c.CustomResponseModifier = func(resp *guardcore.Response) *guardcore.Response {
			t.Error("a hijacked connection must not reach the modifier")
			return resp
		}
	})
	guard, err := New(engine)
	if err != nil {
		t.Fatal(err)
	}
	router := echolib.New()
	router.Use(guard)
	router.Any("/*", func(c *echolib.Context) error {
		conn, _, err := http.NewResponseController(c.Response()).Hijack()
		if err != nil {
			return err
		}
		return conn.Close()
	})
	router.ServeHTTP(hijackRecorder{httptest.NewRecorder()}, httptest.NewRequest("GET", "/ws", nil))
}
