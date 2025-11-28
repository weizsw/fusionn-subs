package logger

import (
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.SugaredLogger

func Init(isDev bool) {
	var encoder zapcore.Encoder
	var level zapcore.Level

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:       "time",
		LevelKey:      "level",
		MessageKey:    "msg",
		StacktraceKey: "", // Hide stacktrace in normal logs
		EncodeTime:    customTimeEncoder,
		EncodeCaller:  nil, // Hide caller
	}

	if isDev {
		// Development: colorful console output
		level = zapcore.DebugLevel
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoderConfig.ConsoleSeparator = " "
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		// Production: clean console output (no JSON)
		level = zapcore.InfoLevel
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		encoderConfig.ConsoleSeparator = " "
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		level,
	)

	logger := zap.New(core)
	Log = logger.Sugar()
}

// customTimeEncoder formats time as "2006-01-02 15:04:05" for logs
func customTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format("2006-01-02 15:04:05"))
}

func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}

// Convenience methods with nil safety
func Info(args ...interface{}) {
	if Log != nil {
		Log.Info(args...)
	}
}

func Infof(template string, args ...interface{}) {
	if Log != nil {
		Log.Infof(template, args...)
	}
}

func Error(args ...interface{}) {
	if Log != nil {
		Log.Error(args...)
	}
}

func Errorf(template string, args ...interface{}) {
	if Log != nil {
		Log.Errorf(template, args...)
	}
}

func Debug(args ...interface{}) {
	if Log != nil {
		Log.Debug(args...)
	}
}

func Debugf(template string, args ...interface{}) {
	if Log != nil {
		Log.Debugf(template, args...)
	}
}

func Warn(args ...interface{}) {
	if Log != nil {
		Log.Warn(args...)
	}
}

func Warnf(template string, args ...interface{}) {
	if Log != nil {
		Log.Warnf(template, args...)
	}
}

func Fatal(args ...interface{}) {
	if Log != nil {
		Log.Fatal(args...) // zap.Fatal calls os.Exit(1)
	} else {
		os.Exit(1)
	}
}

func Fatalf(template string, args ...interface{}) {
	if Log != nil {
		Log.Fatalf(template, args...) // zap.Fatalf calls os.Exit(1)
	} else {
		os.Exit(1)
	}
}
