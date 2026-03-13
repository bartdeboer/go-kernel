package kernel

import (
	"fmt"
	stdlog "log"
	"os"
	"strings"
)

// LogLevel controls which messages are emitted by the default logger.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

// Logger is the common logging interface used by core and adapters.
//
// The non-formatting methods (Debug/Info/Warn/Error) behave like log.Println:
// they append a single newline. The *f methods ensure exactly one trailing
// newline even if the format already contains one.
type Logger interface {
	Debug(v ...any)
	Debugf(format string, args ...any)
	Info(v ...any)
	Infof(format string, args ...any)
	Warn(v ...any)
	Warnf(format string, args ...any)
	Error(v ...any)
	Errorf(format string, args ...any)
}

// logger is the process-wide logger used by the core and all adapters.
// It defaults to a stdlib-based logger without timestamps/prefixes.
var (
	logger   Logger   = newStdLogger(stdlog.New(os.Stderr, "", 0))
	logLevel LogLevel = LogInfo
)

// Log returns the current global Logger.
func Log() Logger {
	return logger
}

// SetLogger overrides the global Logger. Passing nil restores the default logger.
func SetLogger(l Logger) {
	if l == nil {
		logger = newStdLogger(stdlog.New(os.Stderr, "", 0))
		return
	}
	logger = l
}

// SetLogLevel changes the verbosity for the default std logger.
func SetLogLevel(level LogLevel) {
	logLevel = level
}

// CurrentLogLevel returns the current default log level.
func CurrentLogLevel() LogLevel {
	return logLevel
}

// sprintfln formats like fmt.Sprintf but guarantees exactly one trailing newline.
func sprintfln(format string, args ...any) string {
	return strings.TrimRight(fmt.Sprintf(format, args...), "\n") + "\n"
}

// stdLogger is a basic implementation of Logger using log.Logger.
type stdLogger struct {
	l *stdlog.Logger
}

func newStdLogger(l *stdlog.Logger) *stdLogger {
	return &stdLogger{l: l}
}

func (s *stdLogger) Debug(v ...any) {
	if logLevel > LogDebug {
		return
	}
	s.l.Println(v...)
}

func (s *stdLogger) Debugf(format string, args ...any) {
	if logLevel > LogDebug {
		return
	}
	s.l.Print(sprintfln(format, args...))
}

func (s *stdLogger) Info(v ...any) {
	if logLevel > LogInfo {
		return
	}
	s.l.Println(v...)
}

func (s *stdLogger) Infof(format string, args ...any) {
	if logLevel > LogInfo {
		return
	}
	s.l.Print(sprintfln(format, args...))
}

func (s *stdLogger) Warn(v ...any) {
	if logLevel > LogWarn {
		return
	}
	s.l.Println(v...)
}

func (s *stdLogger) Warnf(format string, args ...any) {
	if logLevel > LogWarn {
		return
	}
	s.l.Print(sprintfln(format, args...))
}

func (s *stdLogger) Error(v ...any) {
	if logLevel > LogError {
		return
	}
	s.l.Println(v...)
}

func (s *stdLogger) Errorf(format string, args ...any) {
	if logLevel > LogError {
		return
	}
	s.l.Print(sprintfln(format, args...))
}
