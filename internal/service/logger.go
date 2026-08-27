package service

import "log/slog"

// loggerType keeps the Service struct readable while still using the standard
// library's logger directly.
type loggerType = slog.Logger
