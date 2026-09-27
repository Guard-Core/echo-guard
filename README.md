# echo-guard

Echo middleware adapter for [guard-core-go](https://github.com/rennf93/guard-core-go). Translates `echo.Context` into the guardcore request surface, runs the engine, and translates verdicts to exact Echo responses (status, headers, body, then stop the chain). Works with any `echo.Echo` or `echo.Group` chain via `e.Use`.

Docs: <https://rennf93.github.io/echo-guard/>

## Install

Released: `v1.2.0` on the Go module proxy, flooring the guard-core-go v4.2.0 parity engine:

```
go get github.com/rennf93/echo-guard@v1.2.0 github.com/rennf93/guard-core-go/v4@v4.2.0
```

The package name is `echo`, which collides with `github.com/labstack/echo/v4` (also package `echo`), so import the adapter with an explicit alias such as `guardecho`.

## Usage

```go
package main

import (
	"log"

	echolib "github.com/labstack/echo/v4"
	guardcore "github.com/rennf93/guard-core-go/v4/guardcore"
	guardecho "github.com/rennf93/echo-guard"
)

func main() {
	cfg := guardcore.DefaultSecurityConfig()
	engine, err := guardcore.NewEngine(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := engine.Initialize(); err != nil {
		log.Fatal(err)
	}

	guard, err := guardecho.New(engine)
	if err != nil {
		log.Fatal(err)
	}

	router := echolib.New()
	router.Use(guard)
	router.GET("/", func(c echolib.Context) error {
		return c.String(200, "ok")
	})

	log.Fatal(router.Start(":8080"))
}
```

Options: `guardecho.WithMaxBodyBytes(n)` bounds the body bytes the engine scans (default 262144), `guardecho.WithLogger(l)` swaps the fail-closed logger. Route-level configuration uses `engine.Routes.Register` plus `guardecho.WithRouteID(ctx, id)` on the request context (set the wrapped request with `c.SetRequest` in a middleware registered before the guard).

Every engine `SecurityConfig` field is reachable through this adapter: global tuning (behavior rules with `BehaviorScanResponseBody` and the inspect-bytes budget, the geo lifecycle with `IPInfoToken`/`OnGeoEvent`, CORS, security headers, custom error bodies) goes through the `SecurityConfig` you hand to `guardcore.NewEngine`, per-route detection exclusions and per-route behavior/IP rules through `engine.Routes.Register`. On every pass-through response the adapter merges `Engine.ResponseHeaders()` with `Engine.CORSResponseHeaders(req)` and reports the response (status plus the leading inspect-budget bytes of the body when `BehaviorScanResponseBody` is on) to `Engine.ProcessResponse` for the behavioral return rules. See [docs/configuration.md](docs/configuration.md).

Echo's middleware contract has no explicit abort call: a verdict is written to `c.Response()` and the chain stops by not calling `next(c)`. Engine malfunctions fail closed with a 500. Detection covers at most the first `MaxBodyBytes` of the body; payloads beyond the bound are not scanned, and the full body still reaches your handler untouched.

## Development

The middleware consumes the core as a normal module dependency, currently pinned to the guard-core-go master surface (`v4.0.5-0.20260926230539-e39ac203568b`, the behavior-rules / geo-lifecycle / route-detection-exclusions wave); no `replace` directive is used or needed. For cross-repo work on the core itself, add a temporary local `replace` line in your own checkout and drop it before committing.

Integration tests run against real Redis:

```
REDIS_HOST=127.0.0.1 go test -tags integration ./...
```

## License

MIT
