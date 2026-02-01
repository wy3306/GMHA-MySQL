package logger

import (
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
// 一套集群对应一个日志文件；通过 ClusterID 区分多套 MHA，日志路径为 LogDir/ClusterID/LogFile。
type Config struct {
	// 集群标识，多套 MHA 时必填；日志将写入 LogDir/ClusterID/LogFile，且每条日志带 cluster_id 字段
	ClusterID string
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
	// 一套集群一个目录：LogDir/ClusterID；无 ClusterID 时用 LogDir
	logDir := cfg.LogDir
	if cfg.ClusterID != "" {
		logDir = filepath.Join(cfg.LogDir, cfg.ClusterID)
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	filename := filepath.Join(logDir, cfg.LogFile)
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

	base := zerolog.New(out).With().Timestamp().Logger()
	if cfg.ClusterID != "" {
		globalLogger = base.With().Str("cluster_id", cfg.ClusterID).Logger()
	} else {
		globalLogger = base
	}

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
		return zerolog.New(os.Stdout).With().Timestamp().Logger()
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
