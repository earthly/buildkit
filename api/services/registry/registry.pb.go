// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: registry.proto

package earthly_registry_v1

import (
	context "context"
	fmt "fmt"
	proto "github.com/gogo/protobuf/proto"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

type ByteMessage struct {
	Data                 []byte   `protobuf:"bytes,1,opt,name=data,proto3" json:"data,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ByteMessage) Reset()         { *m = ByteMessage{} }
func (m *ByteMessage) String() string { return proto.CompactTextString(m) }
func (*ByteMessage) ProtoMessage()    {}
func (*ByteMessage) Descriptor() ([]byte, []int) {
	return fileDescriptor_41af05d40a615591, []int{0}
}
func (m *ByteMessage) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ByteMessage.Unmarshal(m, b)
}
func (m *ByteMessage) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ByteMessage.Marshal(b, m, deterministic)
}
func (m *ByteMessage) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ByteMessage.Merge(m, src)
}
func (m *ByteMessage) XXX_Size() int {
	return xxx_messageInfo_ByteMessage.Size(m)
}
func (m *ByteMessage) XXX_DiscardUnknown() {
	xxx_messageInfo_ByteMessage.DiscardUnknown(m)
}

var xxx_messageInfo_ByteMessage proto.InternalMessageInfo

func (m *ByteMessage) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func init() {
	proto.RegisterType((*ByteMessage)(nil), "earthly.registry.v1.ByteMessage")
}

func init() { proto.RegisterFile("registry.proto", fileDescriptor_41af05d40a615591) }

var fileDescriptor_41af05d40a615591 = []byte{
	// 123 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0xe2, 0x2b, 0x4a, 0x4d, 0xcf,
	0x2c, 0x2e, 0x29, 0xaa, 0xd4, 0x2b, 0x28, 0xca, 0x2f, 0xc9, 0x17, 0x12, 0x4e, 0x4d, 0x2c, 0x2a,
	0xc9, 0xc8, 0xa9, 0xd4, 0x83, 0x8b, 0x97, 0x19, 0x2a, 0x29, 0x72, 0x71, 0x3b, 0x55, 0x96, 0xa4,
	0xfa, 0xa6, 0x16, 0x17, 0x27, 0xa6, 0xa7, 0x0a, 0x09, 0x71, 0xb1, 0xa4, 0x24, 0x96, 0x24, 0x4a,
	0x30, 0x2a, 0x30, 0x6a, 0xf0, 0x04, 0x81, 0xd9, 0x46, 0xd1, 0x5c, 0x1c, 0x41, 0x50, 0x1d, 0x42,
	0xfe, 0x5c, 0xac, 0x01, 0x45, 0xf9, 0x15, 0x95, 0x42, 0x0a, 0x7a, 0x58, 0x4c, 0xd3, 0x43, 0x32,
	0x4a, 0x8a, 0xa0, 0x0a, 0x0d, 0x46, 0x03, 0xc6, 0x24, 0x36, 0xb0, 0xdb, 0x8c, 0x01, 0x01, 0x00,
	0x00, 0xff, 0xff, 0xe2, 0x5a, 0x7f, 0x8a, 0xad, 0x00, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// RegistryClient is the client API for Registry service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type RegistryClient interface {
	Proxy(ctx context.Context, opts ...grpc.CallOption) (Registry_ProxyClient, error)
}

type registryClient struct {
	cc *grpc.ClientConn
}

func NewRegistryClient(cc *grpc.ClientConn) RegistryClient {
	return &registryClient{cc}
}

func (c *registryClient) Proxy(ctx context.Context, opts ...grpc.CallOption) (Registry_ProxyClient, error) {
	stream, err := c.cc.NewStream(ctx, &_Registry_serviceDesc.Streams[0], "/earthly.registry.v1.Registry/Proxy", opts...)
	if err != nil {
		return nil, err
	}
	x := &registryProxyClient{stream}
	return x, nil
}

type Registry_ProxyClient interface {
	Send(*ByteMessage) error
	Recv() (*ByteMessage, error)
	grpc.ClientStream
}

type registryProxyClient struct {
	grpc.ClientStream
}

func (x *registryProxyClient) Send(m *ByteMessage) error {
	return x.ClientStream.SendMsg(m)
}

func (x *registryProxyClient) Recv() (*ByteMessage, error) {
	m := new(ByteMessage)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// RegistryServer is the server API for Registry service.
type RegistryServer interface {
	Proxy(Registry_ProxyServer) error
}

// UnimplementedRegistryServer can be embedded to have forward compatible implementations.
type UnimplementedRegistryServer struct {
}

func (*UnimplementedRegistryServer) Proxy(srv Registry_ProxyServer) error {
	return status.Errorf(codes.Unimplemented, "method Proxy not implemented")
}

func RegisterRegistryServer(s *grpc.Server, srv RegistryServer) {
	s.RegisterService(&_Registry_serviceDesc, srv)
}

func _Registry_Proxy_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(RegistryServer).Proxy(&registryProxyServer{stream})
}

type Registry_ProxyServer interface {
	Send(*ByteMessage) error
	Recv() (*ByteMessage, error)
	grpc.ServerStream
}

type registryProxyServer struct {
	grpc.ServerStream
}

func (x *registryProxyServer) Send(m *ByteMessage) error {
	return x.ServerStream.SendMsg(m)
}

func (x *registryProxyServer) Recv() (*ByteMessage, error) {
	m := new(ByteMessage)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

var _Registry_serviceDesc = grpc.ServiceDesc{
	ServiceName: "earthly.registry.v1.Registry",
	HandlerType: (*RegistryServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Proxy",
			Handler:       _Registry_Proxy_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "registry.proto",
}
