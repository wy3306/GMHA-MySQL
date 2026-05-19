// Package grpc 提供 GMHA 的 gRPC 服务端实现，负责处理 Agent 心跳流式通信。
package grpc

import (
	"context"
	"io"
	"log"
	"net"
	"time"

	"gmha/internal/app"
	hbgrpc "gmha/pkg/rpc/heartbeat"
	"google.golang.org/grpc"
)

// HeartbeatServer 是 gRPC 心跳服务的实现，负责接收和处理 Agent 的心跳请求。
type HeartbeatServer struct {
	service *app.HeartbeatService
}

// NewHeartbeatServer 创建一个新的 HeartbeatServer 实例。
func NewHeartbeatServer(service *app.HeartbeatService) *HeartbeatServer {
	return &HeartbeatServer{service: service}
}

// StreamHeartbeat 处理 Agent 的双向心跳流，接收请求并返回响应。
func (s *HeartbeatServer) StreamHeartbeat(stream hbgrpc.HeartbeatService_StreamHeartbeatServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		resp, err := s.service.ProcessHeartbeat(stream.Context(), req)
		if err != nil {
			return err
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// Serve 在指定地址启动 gRPC 心跳服务。
func Serve(ctx context.Context, service *app.HeartbeatService, listen string) error {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	return ServeListener(ctx, service, lis)
}

// ServeListener 在指定监听器上启动 gRPC 心跳服务，并启动定时协调协程。
func ServeListener(ctx context.Context, service *app.HeartbeatService, lis net.Listener) error {
	server := grpc.NewServer(grpc.ForceServerCodec(hbgrpc.JSONCodec{}))
	hbgrpc.RegisterHeartbeatServiceServer(server, NewHeartbeatServer(service))

	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()

	go func() {
		ticker := time.NewTicker(service.TickInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := service.Reconcile(context.Background()); err != nil {
					log.Printf("heartbeat reconcile failed: %v", err)
				}
			}
		}
	}()

	log.Printf("gmha grpc heartbeat listening on %s", lis.Addr().String())
	return server.Serve(lis)
}
