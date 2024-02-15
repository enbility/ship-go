package logging_test

import (
	"testing"

	"github.com/enbility/ship-go/logging"
	"github.com/stretchr/testify/suite"
)

func TestLoggingSuite(t *testing.T) {
	suite.Run(t, new(LogSuite))
}

type LogSuite struct {
	suite.Suite
}

func (l *LogSuite) Trace(args ...interface{})                 {}
func (l *LogSuite) Tracef(format string, args ...interface{}) {}
func (l *LogSuite) Debug(args ...interface{})                 {}
func (l *LogSuite) Debugf(format string, args ...interface{}) {}
func (l *LogSuite) Info(args ...interface{})                  {}
func (l *LogSuite) Infof(format string, args ...interface{})  {}
func (l *LogSuite) Error(args ...interface{})                 {}
func (l *LogSuite) Errorf(format string, args ...interface{}) {}

func (l *LogSuite) Test_Logging() {
	logging.SetLogging(nil)

	logging.Log().Trace("test")
	logging.Log().Tracef("test")
	logging.Log().Debug("test")
	logging.Log().Debugf("test")
	logging.Log().Info("test")
	logging.Log().Infof("test")
	logging.Log().Error("test")
	logging.Log().Errorf("test")

	logging.SetLogging(l)
}
