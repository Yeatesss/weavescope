package httpclient

type ControRes struct {
	Success bool      `json:"success"`
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    NodeLimit `json:"data"`
}
type NodeLimit struct {
	AutoAuthorize  bool     `json:"auto_authorize"`
	Limit          int64    `json:"limit"`
	Nodes          []string `json:"nodes"`
	SlaveCollector []string `json:"slave_collector"`
}
