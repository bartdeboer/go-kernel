package log

import (
	"github.com/bartdeboer/go-kernel"
)

// Usage:
//
//   import kernellog "github.com/bartdeboer/go-kernel/log"
//
//   kernellog.Printf("hello %s", name)
//   kernellog.Debugf("details: %#v", v)

func Debug(v ...any)            { kernel.Log().Debug(v...) }
func Debugf(f string, a ...any) { kernel.Log().Debugf(f, a...) }
func Info(v ...any)             { kernel.Log().Info(v...) }
func Infof(f string, a ...any)  { kernel.Log().Infof(f, a...) }
func Warn(v ...any)             { kernel.Log().Warn(v...) }
func Warnf(f string, a ...any)  { kernel.Log().Warnf(f, a...) }
func Error(v ...any)            { kernel.Log().Error(v...) }
func Errorf(f string, a ...any) { kernel.Log().Errorf(f, a...) }
func Print(a ...any)            { Info(a...) }
func Printf(f string, a ...any) { Infof(f, a...) }
