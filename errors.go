package webserver

import "errors"

// errors
var (
	ErrInvalidRequest        = errors.New("invalid request")
	ErrInvalidAuthentication = errors.New("invalid authentication")
	ErrUnauthorized          = errors.New("unauthorized")
)
