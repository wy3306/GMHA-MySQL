// Package agent 是 GMHA 代理进程的入口包，负责启动心跳上报、任务接收、动态指标采集等核心流程。
package agent

import (
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	agentcollect "gmha/internal/agent/collect"
	agentcore "gmha/internal/agent/core"
	agentdynamic "gmha/internal/agent/dynamic"
	agenthandler "gmha/internal/agent/handler"
	"gmha/internal/agent/mysqlcheck"
	agentmysqldynamic "gmha/internal/agent/mysqldynamic"
	"gmha/internal/agent/selfcheck"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
	hbgrpc "gmha/pkg/rpc/heartbeat"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Run 启动代理主循环，包括注册任务处理器、建立 gRPC 心跳流、启动动态指标采集，并定时发送心跳包。
func Run(ctx context.Context, cfg Config) error {
	dispatcher := agentcore.NewDispatcher(
		agenthandler.NewExecHandler(),
		agenthandler.NewCollectMachineInfoHandler(agentcollect.NewMachineCollector()),
		agenthandler.NewCollectStaticInfoHandler(agentcollect.NewStaticCollector(cfg.InstallDir)),
		agenthandler.NewMySQLInstallHandler(cfg.ManagerHTTPAddr, cfg.InstallDir),
		agenthandler.NewMySQLUninstallHandler(cfg.InstallDir),
		agenthandler.NewMySQLTopologyHandler(),
	)
	receiver := agentcore.NewReceiver(cfg.ManagerHTTPAddr, cfg.AgentID, cfg.MachineID, dispatcher)
	go func() {
		_ = receiver.Run(ctx)
	}()

	conn, err := grpc.DialContext(
		ctx,
		cfg.ManagerGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(hbgrpc.JSONCodec{})),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := hbgrpc.NewHeartbeatServiceClient(conn)
	stream, err := client.StreamHeartbeat(ctx)
	if err != nil {
		return err
	}

	registry := agentdynamic.NewCollectorRegistry()
	mysqlConfigPath := filepath.Join(cfg.InstallDir, mysqlcheck.DefaultConfigFile)
	agentdynamic.RegisterBuiltinCollectors(registry, mysqlConfigPath)
	dynamicManager := agentdynamic.NewDynamicCollectManager(cfg.AgentID, registry)
	dynamicManager.Start(ctx, dynamicdomain.BuildDefaultDynamicCollectConfig())
	defer dynamicManager.StopDynamicCollectors()

	mysqlRegistry := agentmysqldynamic.NewCollectorRegistry()
	agentmysqldynamic.RegisterBuiltinMySQLCollectors(mysqlRegistry)
	mysqlDynamicManager := agentmysqldynamic.NewMultiInstanceMySQLDynamicCollectManager(cfg.AgentID, mysqlRegistry, func() ([]*agentmysqldynamic.CollectEnv, error) {
		return agentmysqldynamic.BuildCollectEnvs(mysqlConfigPath)
	})
	mysqlDynamicManager.Start(ctx, dynamicdomain.BuildDefaultMySQLDynamicCollectConfig())
	defer mysqlDynamicManager.StopMySQLDynamicCollectors()

	go func() {
		for {
			resp, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr != io.EOF {
					return
				}
				return
			}
			if resp.DynamicCollect != nil {
				dynamicManager.UpdateCollectConfig(ctx, *resp.DynamicCollect)
			}
			if resp.MySQLDynamicCollect != nil {
				mysqlDynamicManager.UpdateMySQLDynamicCollectConfig(ctx, *resp.MySQLDynamicCollect)
			}
		}
	}()

	hostname, _ := os.Hostname()
	startedAt := time.Now().UTC()
	bootID := startedAt.Format("20060102T150405") + "-" + hostname
	streamID := startedAt.Format("20060102T150405") + "-" + randString(8)
	checker := selfcheck.NewChecker(cfg.InstallDir)
	ticker := time.NewTicker(cfg.HeartbeatInterval)
	defer ticker.Stop()

	var seq uint64
	for {
		overall, summary, checks := checker.Run(ctx)
		req := &hbgrpc.HeartbeatRequest{
			Identity: hbgrpc.AgentIdentity{
				AgentID:   cfg.AgentID,
				MachineID: cfg.MachineID,
				Hostname:  hostname,
				Version:   "0.1.0",
				BootID:    bootID,
			},
			Runtime: hbgrpc.AgentRuntime{
				SentAtUnixMS:        time.Now().UTC().UnixMilli(),
				Seq:                 seq,
				UptimeSec:           uint64(time.Since(startedAt).Seconds()),
				HeartbeatIntervalMS: uint32(cfg.HeartbeatInterval.Milliseconds()),
				StreamID:            streamID,
			},
			Health: hbgrpc.AgentHealth{
				Overall: string(overall),
				Summary: summary,
				Checks:  mapChecks(checks),
			},
			Metrics:      dynamicManager.LastBatch(),
			MySQLMetrics: mysqlDynamicManager.LastBatch(),
		}
		if err := stream.Send(req); err != nil {
			return err
		}
		seq++
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// mapChecks 将领域层健康检查结果转换为 gRPC 心跳协议的健康检查格式。
func mapChecks(items []hbdomain.HealthCheck) []hbgrpc.HealthCheck {
	out := make([]hbgrpc.HealthCheck, 0, len(items))
	for _, item := range items {
		out = append(out, hbgrpc.HealthCheck{
			Name:            item.Name,
			Status:          string(item.Status),
			Detail:          item.Detail,
			CheckedAtUnixMS: item.CheckedAt.UnixMilli(),
		})
	}
	return out
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, n)
	for i := range out {
		out[i] = letters[rand.Intn(len(letters))]
	}
	return string(out)
}
