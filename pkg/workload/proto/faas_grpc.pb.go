// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package proto

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// ExecutorClient is the client API for Executor service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ExecutorClient interface {
	// Remote execution via RPC call.
	Execute(ctx context.Context, in *FaasRequest, opts ...grpc.CallOption) (*FaasReply, error)
}

type executorClient struct {
	cc grpc.ClientConnInterface
}

func NewExecutorClient(cc grpc.ClientConnInterface) ExecutorClient {
	return &executorClient{cc}
}

func (c *executorClient) Execute(ctx context.Context, in *FaasRequest, opts ...grpc.CallOption) (*FaasReply, error) {
	out := new(FaasReply)
	err := c.cc.Invoke(ctx, "/faas.Executor/Execute", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ExecutorServer is the server API for Executor service.
// All implementations must embed UnimplementedExecutorServer
// for forward compatibility
type ExecutorServer interface {
	// Remote execution via RPC call.
	Execute(context.Context, *FaasRequest) (*FaasReply, error)
	mustEmbedUnimplementedExecutorServer()
}

// UnimplementedExecutorServer must be embedded to have forward compatible implementations.
type UnimplementedExecutorServer struct {
}

func (UnimplementedExecutorServer) Execute(context.Context, *FaasRequest) (*FaasReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Execute not implemented")
}
func (UnimplementedExecutorServer) mustEmbedUnimplementedExecutorServer() {}

// UnsafeExecutorServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ExecutorServer will
// result in compilation errors.
type UnsafeExecutorServer interface {
	mustEmbedUnimplementedExecutorServer()
}

func RegisterExecutorServer(s grpc.ServiceRegistrar, srv ExecutorServer) {
	s.RegisterService(&Executor_ServiceDesc, srv)
}

func _Executor_Execute_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(FaasRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ExecutorServer).Execute(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/faas.Executor/Execute",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ExecutorServer).Execute(ctx, req.(*FaasRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Executor_ServiceDesc is the grpc.ServiceDesc for Executor service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Executor_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "faas.Executor",
	HandlerType: (*ExecutorServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Execute",
			Handler:    _Executor_Execute_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "faas.proto",
}
