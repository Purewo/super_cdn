package server

import (
	"supercdn/internal/urlredact"
)

func redactDiagnosticURL(raw string) (string, bool) {
	return urlredact.RedactAllQueryValues(raw)
}
