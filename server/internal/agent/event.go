package agent

// Event 统一事件协议：Python Agent 产出，网关按传输层封装为 WS/SSE 帧透传。
// 只保留「事件名 + 任意数据」两层结构，新增事件类型无需改协议。
type Event struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// 预定义事件类型，与 Python Agent 侧约定一致；网关只透传，不解释 data。
// 其余事件名（token / tool_start / tool_end / card / table / sources / ping / done）
// 仅作字符串转发，完整清单见 agent/app/main.py 顶部注释。
const (
	EventStatus = "status" // 运行状态提示，如「正在思考…」
	EventError  = "error"  // 结构化错误，不断连
)

// ErrorData 错误事件载荷；code 供前端分支处理（限流/配额/上游故障），msg 供展示。
type ErrorData struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// RunRequest 一次运行的入参；WS 场景 session_id 可省略（回退握手参数），
// SSE 场景随请求体传入。
type RunRequest struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
}
