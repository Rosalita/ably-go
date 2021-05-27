package ably

import (
	"fmt"
	"log"
	"os"
)

type LogLevel uint

const (
	LogNone LogLevel = iota
	LogError
	LogWarning
	LogInfo
	LogVerbose
	LogDebug
)

var logLevels = map[LogLevel]string{
	LogError:   "[ERROR] ",
	LogWarning: "[WARN] ",
	LogInfo:    "[INFO] ",
	LogVerbose: "[VERBOSE] ",
	LogDebug:   "[DEBUG] ",
}

var defaultLog = LoggerOptions{
	Logger: &StdLogger{Logger: log.New(os.Stderr, "", log.LstdFlags)},
	Level:  LogNone,
}

// LoggerOptions defines options for ably logging.
type LoggerOptions struct {
	Logger Logger
	Level  LogLevel
}

func (l LoggerOptions) Is(level LogLevel) bool {
	return l.Level != LogNone && l.Level >= level
}

func (l LoggerOptions) Print(level LogLevel, v ...interface{}) {
	if l.Is(level) {
		l.GetLogger().Print(level, v...)
	}
}

func (l LoggerOptions) Printf(level LogLevel, format string, v ...interface{}) {
	if l.Is(level) {
		l.GetLogger().Printf(level, format, v...)
	}
}

// GetLogger returns the custom logger if any. This will return the default
// logger if custom logger was not specified.
func (l LoggerOptions) GetLogger() Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return defaultLog.Logger
}

func (l LoggerOptions) sugar() *sugaredLogger {
	return &sugaredLogger{LoggerOptions: l}
}

// Logger is an interface for ably loggers.
type Logger interface {
	Print(level LogLevel, v ...interface{})
	Printf(level LogLevel, format string, v ...interface{})
}

// StdLogger wraps log.Logger to satisfy the Logger interface.
type StdLogger struct {
	*log.Logger
}

func (s *StdLogger) Printf(level LogLevel, format string, v ...interface{}) {
	s.Logger.Printf(logLevels[level]+format, v...)
}

func (s *StdLogger) Print(level LogLevel, v ...interface{}) {
	if len(v) != 0 {
		v[0] = fmt.Sprintf(logLevels[level]+"%v", v[0])
		s.Logger.Print(v...)
	}
}

type sugaredLogger struct {
	LoggerOptions
}

func (s sugaredLogger) Error(v ...interface{}) {
	s.Print(LogError, v...)
}

func (s sugaredLogger) Errorf(fmt string, v ...interface{}) {
	s.Printf(LogError, fmt, v...)
}

func (s sugaredLogger) Warn(v ...interface{}) {
	s.Print(LogWarning, v...)
}

func (s sugaredLogger) Warnf(fmt string, v ...interface{}) {
	s.Printf(LogWarning, fmt, v...)
}

func (s sugaredLogger) Info(v ...interface{}) {
	s.Print(LogInfo, v...)
}

func (s sugaredLogger) Infof(fmt string, v ...interface{}) {
	s.Printf(LogInfo, fmt, v...)
}

func (s sugaredLogger) Verbose(v ...interface{}) {
	s.Print(LogVerbose, v...)
}

func (s sugaredLogger) Verbosef(fmt string, v ...interface{}) {
	s.Printf(LogVerbose, fmt, v...)
}

func (s sugaredLogger) Debugf(fmt string, v ...interface{}) {
	s.Printf(LogDebug, fmt, v...)
}

func (s sugaredLogger) Debug(v ...interface{}) {
	s.Print(LogDebug, v...)
}
