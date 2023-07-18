package controls

type ControlAction struct {
	Type   string //对象类型 container:容器
	Action string //操作：pause:暂停容器 unpause：解除暂停 stop:停止 remove:删除
	ID     string //container_id || image_id
	Resp   chan interface{}
}
