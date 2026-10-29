# bugtracking-go

Go client for the BugTrack ingest API. It reports errors and panics from a standard `net/http` service or a Gin service.

The module path is `github.com/cipl-sheik/bugtracking-go`.

## Install

```bash
go get github.com/cipl-sheik/bugtracking-go
```

Go 1.22 or newer.

## Configure

Create a client with the project DSN, or with the API origin and project API key. The key must include the `ingest` scope. The project is taken from the key. Do not send a project id.

```go
client, err := bugtrack.New(bugtrack.Options{
    DSN:         os.Getenv("BUGTRACK_DSN"),
    Environment: "production",
    Release:     "1.4.2",
})
```

DSN forms:

```text
http://btk_<prefix>_<secret>@host:8080/42
https://<prefix>:<secret>@host/42
```

The number in the path is ignored. The same client can be created without a DSN:

```go
client, err := bugtrack.New(bugtrack.Options{
    Endpoint:    "http://127.0.0.1:8080",
    APIKey:      os.Getenv("BUGTRACK_KEY"),
    Environment: "production",
})
```

`New` checks the configuration and does not call the API.

## Report an error

```go
result, err := client.Capture(ctx, err, bugtrack.WithTag("queue", "billing"))
```

A nil error is ignored. `Capture` returns a transport or API failure to the caller and does not panic.

```go
client.CaptureMessage(ctx, "checkout queue is behind")
```

`Capture` uses level `error`. `CaptureMessage` uses level `info`. `CapturePanic` and `Recover` use level `fatal`. Override one event with `bugtrack.WithLevel`.

Other event options: `WithTag`, `WithTags`, `WithUser`, `WithExtra`, `WithRequest`, and `WithTransaction`.

## net/http

`HTTPMiddleware` reports a panic, then re-panics so the service's own recovery still runs. Put that recovery outside this middleware if the response should be a normal 500.

```go
handler := client.HTTPMiddleware(app)
http.ListenAndServe(":8080", handler)
```

Inside a goroutine:

```go
defer client.Recover(ctx)
```

## Gin

Register this middleware after `gin.Recovery`. `gin.Default` already registers recovery first, so a later `r.Use` is the right order. BugTrack reports the panic and re-panics. Gin then writes the 500.

```go
func BugTrack(client *bugtrack.Client) gin.HandlerFunc {
    return func(c *gin.Context) {
        defer client.Recover(c.Request.Context(),
            bugtrack.WithRequest(bugtrack.RequestFields(c.Request)),
            bugtrack.WithTransaction(c.Request.Method+" "+c.FullPath()),
        )
        c.Next()
    }
}
```

```go
r := gin.Default()
r.Use(BugTrack(client))
```

An error returned with `c.Error` or `c.JSON` is not a panic. Report that error with `Capture`.

## What is sent

Events are posted to `POST /api/v1/ingest` with the `X-BugTrack-Key` header. Each event sets `platform` to `go` and `sdk.name` to `bugtrack-go`. The client generates `event_id`, so one retry stays one event.

A successful response is HTTP 202:

```json
{
  "event_id": 45,
  "issue_id": 9,
  "event_uuid": "..."
}
```

Query strings are omitted. `Authorization`, `Cookie`, and API-key headers are omitted. Do not put secrets in tags, user, extra, or request fields.

A 500 or a dropped connection is retried once with the same `event_id`. A 429 is retried once, waiting at most 2 seconds. Each attempt times out after 2 seconds unless `Options.Timeout` is set. Status 401, 403, 409, and 422 are not retried.

## Develop

```bash
go test ./...
```
