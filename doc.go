// Package bugtrack reports Go errors and panics to a BugTrack API.
//
// The client posts a language-agnostic event to POST /api/v1/ingest. The
// project is identified by the API key. Do not send a project id in the body.
//
// Configure with a project DSN or with an API origin plus key:
//
//	client, err := bugtrack.New(bugtrack.Options{
//		DSN:         os.Getenv("BUGTRACK_DSN"), // http://btk_<prefix>_<secret>@host:8080/42
//		Environment: "production",
//		Release:     "1.4.2",
//	})
//
//	result, err := client.Capture(ctx, err)
//
// Capture returns transport and API failures to the caller and does not panic.
// HTTPMiddleware reports a panic, then re-panics so the application's own
// recovery still runs.
//
// DSN forms accepted by the API:
//
//	http://btk_<prefix>_<secret>@host:8080/<projectID>
//	https://<prefix>:<secret>@host/<projectID>
//
// The path segment is ignored. The key must include the ingest scope.
// Authentication is sent in the X-BugTrack-Key header.
package bugtrack
