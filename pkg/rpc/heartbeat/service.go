package heartbeat

import (
	"context"

	"google.golang.org/grpc"
)

// streamHeartbeatMethod 是心跳流式 RPC 方法的完整路径。
const streamHeartbeatMethod = "/gmha.agent.v1.HeartbeatService/StreamHeartbeat"

// HeartbeatServiceClient 定义了心跳服务的客户端接口，提供双向流式心跳通信能力。
type HeartbeatServiceClient interface {
	StreamHeartbeat(ctx context.Context, opts ...grpc.CallOption) (HeartbeatService_StreamHeartbeatClient, error)
}

// heartbeatServiceClient 是 HeartbeatServiceClient 接口的实现，封装了 gRPC 客户端连接。
type heartbeatServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewHeartbeatServiceClient 创建一个新的心跳服务客户端实例。
func NewHeartbeatServiceClient(cc grpc.ClientConnInterface) HeartbeatServiceClient {
	return &heartbeatServiceClient{cc: cc}
}

// StreamHeartbeat 建立与服务端的双向流式心跳连接，返回流式客户端用于发送和接收心跳消息。
func (c *heartbeatServiceClient) StreamHeartbeat(ctx context.Context, opts ...grpc.CallOption) (HeartbeatService_StreamHeartbeatClient, error) {
	stream, err := c.cc.NewStream(ctx, &HeartbeatService_ServiceDesc.Streams[0], streamHeartbeatMethod, opts...)
	if err != nil {
		return nil, err
	}
	return &heartbeatServiceStreamHeartbeatClient{ClientStream: stream}, nil
}

// HeartbeatService_StreamHeartbeatClient 定义了心跳流式客户端接口，支持发送请求和接收响应。
type HeartbeatService_StreamHeartbeatClient interface {
	Send(*HeartbeatRequest) error
	Recv() (*HeartbeatResponse, error)
	grpc.ClientStream
}

// heartbeatServiceStreamHeartbeatClient 是 HeartbeatService_StreamHeartbeatClient 接口的实现。
type heartbeatServiceStreamHeartbeatClient struct {
	grpc.ClientStream
}

// Send 向服务端发送心跳请求消息。
func (c *heartbeatServiceStreamHeartbeatClient) Send(req *HeartbeatRequest) error {
	return c.ClientStream.SendMsg(req)
}

// Recv 从服务端接收心跳响应消息。
func (c *heartbeatServiceStreamHeartbeatClient) Recv() (*HeartbeatResponse, error) {
	resp := new(HeartbeatResponse)
	if err := c.ClientStream.RecvMsg(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// HeartbeatServiceServer 定义了心跳服务的服务端接口，处理双向流式心跳请求。
type HeartbeatServiceServer interface {
	StreamHeartbeat(HeartbeatService_StreamHeartbeatServer) error
}

// HeartbeatService_StreamHeartbeatServer 定义了心跳流式服务端接口，支持接收请求和发送响应。
type HeartbeatService_StreamHeartbeatServer interface {
	Send(*HeartbeatResponse) error
	Recv() (*HeartbeatRequest, error)
	grpc.ServerStream
}

// heartbeatServiceStreamHeartbeatServer 是 HeartbeatService_StreamHeartbeatServer 接口的实现。
type heartbeatServiceStreamHeartbeatServer struct {
	grpc.ServerStream
}

// Send 向客户端发送心跳响应消息。
func (s *heartbeatServiceStreamHeartbeatServer) Send(resp *HeartbeatResponse) error {
	return s.ServerStream.SendMsg(resp)
}

// Recv 从客户端接收心跳请求消息。
func (s *heartbeatServiceStreamHeartbeatServer) Recv() (*HeartbeatRequest, error) {
	req := new(HeartbeatRequest)
	if err := s.ServerStream.RecvMsg(req); err != nil {
		return nil, err
	}
	return req, nil
}

// RegisterHeartbeatServiceServer 将心跳服务注册到 gRPC 服务注册器中。
func RegisterHeartbeatServiceServer(s grpc.ServiceRegistrar, srv HeartbeatServiceServer) {
	s.RegisterService(&HeartbeatService_ServiceDesc, srv)
}

// HeartbeatService_ServiceDesc 是心跳服务的 gRPC 服务描述符，定义了服务名称和流式处理方法。
var HeartbeatService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gmha.agent.v1.HeartbeatService",
	HandlerType: (*HeartbeatServiceServer)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "StreamHeartbeat",
			Handler:       _HeartbeatService_StreamHeartbeat_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
}

// _HeartbeatService_StreamHeartbeat_Handler 是心跳流式 RPC 的服务端处理器，将请求分发到具体的服务实现。
func _HeartbeatService_StreamHeartbeat_Handler(srv any, stream grpc.ServerStream) error {
	return srv.(HeartbeatServiceServer).StreamHeartbeat(&heartbeatServiceStreamHeartbeatServer{ServerStream: stream})
}
