package logging

import "sync"

//go:generate mockery

// LoggingInterface needs to be implemented, if the internal logs should be printed
type LoggingInterface interface {
	Trace(args ...interface{})
	Tracef(format string, args ...interface{})
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
}

// NoLogging is an empty implementation of Logging which does nothing.
type NoLogging struct{}

func (l *NoLogging) Trace(args ...interface{})                 {}
func (l *NoLogging) Tracef(format string, args ...interface{}) {}
func (l *NoLogging) Debug(args ...interface{})                 {}
func (l *NoLogging) Debugf(format string, args ...interface{}) {}
func (l *NoLogging) Info(args ...interface{})                  {}
func (l *NoLogging) Infof(format string, args ...interface{})  {}
func (l *NoLogging) Error(args ...interface{})                 {}
func (l *NoLogging) Errorf(format string, args ...interface{}) {}

var log LoggingInterface = &NoLogging{}
var mux sync.Mutex

// Sets a custom logging implementation
// By default NoLogging is used, so no logs are printed
// This is used by service.SetLogging()
func SetLogging(logger LoggingInterface) {
	if logger == nil {
		return
	}
	mux.Lock()
	defer mux.Unlock()

	log = logger
}

func Log() LoggingInterface {
	mux.Lock()
	defer mux.Unlock()

	return log
}
