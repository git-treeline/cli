package dbsource

import (
	"errors"
	"fmt"
)

// Sentinel errors for failure modes the cmd layer classifies into user-facing
// hints via errors.Is.
var (
	// ErrFlyNotInstalled means the fly CLI is not on PATH.
	ErrFlyNotInstalled = errors.New("fly CLI not found on PATH")
	// ErrFlyNotAuthed means fly could not authenticate against the app.
	ErrFlyNotAuthed = errors.New("fly CLI is not authenticated")
	// ErrHerokuNotInstalled means the heroku CLI is not on PATH.
	ErrHerokuNotInstalled = errors.New("heroku CLI not found on PATH")
	// ErrHerokuNotAuthed means heroku could not authenticate against the app.
	ErrHerokuNotAuthed = errors.New("heroku CLI is not authenticated")
	// ErrMissingURL means an expected postgres:// URL was empty or unset.
	ErrMissingURL = errors.New("postgres URL is empty or missing")
)

// VarNotFoundError means neither the named URL var nor discrete PG* vars were
// found in the resolved environment.
type VarNotFoundError struct {
	Env string
	Var string
	App string
}

func (e *VarNotFoundError) Error() string {
	return fmt.Sprintf("no %s or PG* variables found for source %q (app %s)", e.Var, e.Env, e.App)
}

// UnknownViaError means a source declared an unsupported via value.
type UnknownViaError struct {
	Via string
	Env string
}

func (e *UnknownViaError) Error() string {
	return fmt.Sprintf("unknown source type %q for env %q", e.Via, e.Env)
}

// URLParseError wraps a failure to interpret a postgres:// URL.
type URLParseError struct {
	Reason string
	Err    error
}

func (e *URLParseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Reason, e.Err)
	}
	return e.Reason
}

func (e *URLParseError) Unwrap() error { return e.Err }

// SpecError means a source's config is incomplete (missing app/env field).
type SpecError struct {
	Env     string
	Message string
}

func (e *SpecError) Error() string {
	return fmt.Sprintf("source %q: %s", e.Env, e.Message)
}
