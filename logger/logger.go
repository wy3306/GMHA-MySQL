package logger

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	globalLogger zerolog.Logger
	once         sync.Once
	inited       bool
)

// Config 用于首次初始化时的配置，仅第一次调用 Init 时生效。
// 日志统一写入 LogDir/LogFile（不再包含集群标识字段）。
type Config struct {
	// 日志根目录，如 "log" 或 "/var/log/gmha"
	LogDir string
	// 单集群内主日志文件名，如 "app.log"
	LogFile string
	// 级别: debug, info, warn, error，默认 info
	Level string
	// 是否同时输出到 stdout
	Console bool
	// 单文件最大体积（MB），超过则切割，默认 100；0 表示使用默认值
	MaxSize int
	// 保留最近多少天的备份，超出的删除；0 表示不按天数删除
	MaxAge int
	// 最多保留多少个备份文件，超出的删除；0 表示不限制个数（仍受 MaxAge 约束）
	MaxBackups int
	// 轮转后的旧文件是否 gzip 压缩
	Compress bool
}

// Init 在程序启动时调用一次，完成目录创建、Lumberjack 初始化（切割/压缩/清理）和全局 logger。
// 日志将写入 `LogDir/LogFile`。
// 之后任意模块通过 For(prefix) 或 GetLogger() 使用，不会再次创建文件。
func Init(cfg Config) error {
	var err error
	once.Do(func() {
		err = initLogger(cfg)
		if err == nil {
			inited = true
		}
	})
	return err
}

func initLogger(cfg Config) error {
	if cfg.LogDir == "" {
		cfg.LogDir = "log"
	}
	if cfg.LogFile == "" {
		cfg.LogFile = "app.log"
	}
	// 所有进程写入统一日志目录并使用相同文件名
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return err
	}

	filename := filepath.Join(cfg.LogDir, cfg.LogFile)
	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = 100
	}
	lj := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,
		MaxAge:     cfg.MaxAge,
		MaxBackups: cfg.MaxBackups,
		Compress:   cfg.Compress,
	}

	var writers []io.Writer
	writers = append(writers, lj)
	if cfg.Console {
		writers = append(writers, os.Stdout)
	}
	out := io.MultiWriter(writers...)

	// 将进程名与 pid 作为每条日志的默认字段
	base := zerolog.New(out).With().Timestamp().Int("pid", os.Getpid()).Logger()
	globalLogger = base

	switch cfg.Level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	return nil
}

// GetLogger 返回全局 logger。必须先调用 Init 一次，否则返回仅输出到 stdout 的默认 logger。
func GetLogger() zerolog.Logger {
	if !inited {
		return zerolog.New(os.Stdout).With().Timestamp().Str("process", "").Int("pid", os.Getpid()).Logger()
	}
	return globalLogger
}

// Log 提供类似 fmt 的简单调用：Info(msg)、Infof(format, args)、Error(msg)、Errorf(...) 等。
// 通过 For(prefix) 得到带 component 前缀的 Log，在包顶定义一次即可。
type Log struct {
	z zerolog.Logger
}

// For 返回带 component 前缀的 Log，供各包在开头定义一次，如：var log = logger.For("manager/heartbeat")。
func For(prefix string) Log {
	return Log{z: GetLogger().With().Str("component", prefix).Logger()}
}

func (l Log) Info(msg string)                   { l.z.Info().Msg(msg) }
func (l Log) Infof(format string, args ...any)  { l.z.Info().Msgf(format, args...) }
func (l Log) Warn(msg string)                   { l.z.Warn().Msg(msg) }
func (l Log) Warnf(format string, args ...any)  { l.z.Warn().Msgf(format, args...) }
func (l Log) Error(msg string)                  { l.z.Error().Msg(msg) }
func (l Log) Errorf(format string, args ...any) { l.z.Error().Msgf(format, args...) }
func (l Log) Debug(msg string)                  { l.z.Debug().Msg(msg) }
func (l Log) Debugf(format string, args ...any) { l.z.Debug().Msgf(format, args...) }

// WithErr 返回带 err 字段的链式接口，用于 log.WithErr(err).Errorf("...")
func (l Log) WithErr(err error) Log {
	return Log{z: l.z.With().Err(err).Logger()}
}

// WithField 返回带单个额外字段的 Log，用于在局部上下文添加临时字段
func (l Log) WithField(key string, val any) Log {
	return Log{z: l.z.With().Interface(key, val).Logger()}
}

// WithFields 返回带多个额外字段的 Log
func (l Log) WithFields(fields map[string]any) Log {
	w := l.z.With()
	for k, v := range fields {
		w = w.Interface(k, v)
	}
	return Log{z: w.Logger()}
}

// Context helpers: 支持在 context 中携带 request_id，并将其附加到现有 Log
type ctxKey string

const ctxRequestIDKey ctxKey = "request_id"

// WithRequestID 在 ctx 中设置 request_id
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxRequestIDKey, id)
}

// RequestIDFromContext 从 ctx 中获取 request_id
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v := ctx.Value(ctxRequestIDKey)
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// WithContext 将 ctx 中的 request_id 附加到已有的 Log（不改变原有 component 等字段）
func WithContext(l Log, ctx context.Context) Log {
	if id, ok := RequestIDFromContext(ctx); ok && id != "" {
		return Log{z: l.z.With().Str("request_id", id).Logger()}
	}
	return l
}
