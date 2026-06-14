package dbsource

import (
	"fmt"
	"net/url"
	"strings"
)

// parsePostgresURL parses a postgres:// (or postgresql://) URL into a ConnInfo.
// net/url percent-decodes the userinfo, so passwords must be percent-encoded in
// the source URL; we never decode a second time. A missing port defaults to
// 5432. defaultSSLMode is the explicit config sslmode ("" when unset).
func parsePostgresURL(raw, defaultSSLMode string) (*ConnInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrMissingURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, &URLParseError{
			Reason: "could not parse postgres URL (ensure the password is percent-encoded)",
			Err:    err,
		}
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, &URLParseError{Reason: fmt.Sprintf("unexpected URL scheme %q (want postgres://)", u.Scheme)}
	}
	host := u.Hostname()
	if host == "" {
		return nil, &URLParseError{Reason: "postgres URL has no host"}
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return nil, &URLParseError{Reason: "postgres URL has no database name"}
	}
	password, _ := u.User.Password()
	return &ConnInfo{
		Host:     host,
		Port:     port,
		User:     u.User.Username(),
		Password: password,
		DBName:   dbName,
		SSLMode:  resolveSSLMode(defaultSSLMode, u.Query().Get("sslmode")),
	}, nil
}
