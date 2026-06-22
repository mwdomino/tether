package proto

// Request is the agent → host control message.
type Request struct {
	URL           string `json:"url"`
	LoopbackPorts []int  `json:"loopback_ports,omitempty"`
	AuthToken     string `json:"auth_token,omitempty"`
}

// Response is the host → agent control reply.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TunnelHeader is sent by the host as the first frame of a tunnel substream.
type TunnelHeader struct {
	Kind string `json:"kind"`
	Port int    `json:"port"`
}
