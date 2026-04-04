package natsmonitor

// ConnzResult represents the relevant fields from the NATS /connz endpoint response.
type ConnzResult struct {
	ServerID       string           `json:"server_id"`
	NumConnections int              `json:"num_connections"`
	Connections    []ConnectionInfo `json:"connections"`
}

// ConnectionInfo represents a single client connection from /connz.
// For closed connections (queried via state=closed), the Stop and Reason fields are populated.
type ConnectionInfo struct {
	CID           uint64 `json:"cid"`
	IP            string `json:"ip"`
	Port          int    `json:"port"`
	Account       string `json:"account"`
	NKey          string `json:"nkey"`
	RTT           string `json:"rtt"`
	Subscriptions int    `json:"subscriptions_list"`
	InMsgs        int64  `json:"in_msgs"`
	OutMsgs       int64  `json:"out_msgs"`
	InBytes       int64  `json:"in_bytes"`
	OutBytes      int64  `json:"out_bytes"`
	Uptime        string `json:"uptime"`
	Start         string `json:"start"`
	Stop          string `json:"stop,omitempty"`
	Reason        string `json:"reason,omitempty"`
}
