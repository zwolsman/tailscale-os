package machine

type ServiceStateEvent struct {
	Service string                   `protobuf:"bytes,1,opt,name=service,proto3" json:"service,omitempty"`
	Action  ServiceStateEvent_Action `protobuf:"varint,2,opt,name=action,proto3,enum=machine.ServiceStateEvent_Action" json:"action,omitempty"`
	Message string                   `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
	Health  *ServiceHealth           `protobuf:"bytes,4,opt,name=health,proto3" json:"health,omitempty"`
}

type ServiceStateEvent_Action int32

const (
	ServiceStateEvent_INITIALIZED ServiceStateEvent_Action = 0
	ServiceStateEvent_PREPARING   ServiceStateEvent_Action = 1
	ServiceStateEvent_WAITING     ServiceStateEvent_Action = 2
	ServiceStateEvent_RUNNING     ServiceStateEvent_Action = 3
	ServiceStateEvent_STOPPING    ServiceStateEvent_Action = 4
	ServiceStateEvent_FINISHED    ServiceStateEvent_Action = 5
	ServiceStateEvent_FAILED      ServiceStateEvent_Action = 6
	ServiceStateEvent_SKIPPED     ServiceStateEvent_Action = 7
	ServiceStateEvent_STARTING    ServiceStateEvent_Action = 8
)

type ServiceHealth struct {
	Unknown bool `protobuf:"varint,1,opt,name=unknown,proto3" json:"unknown,omitempty"`
	Healthy bool `protobuf:"varint,2,opt,name=healthy,proto3" json:"healthy,omitempty"`
	// LastMessage string                 `protobuf:"bytes,3,opt,name=last_message,json=lastMessage,proto3" json:"last_message,omitempty"`
	// LastChange  *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=last_change,json=lastChange,proto3" json:"last_change,omitempty"`
}
