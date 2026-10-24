package agent

// Event 统一事件协议：业务层（Python Agent）产出此结构，
// 网关按传输层封装为 WS 帧或 SSE 帧后推给客户端。
// 保持「事件名 + 任意数据」的最小结构，新增事件类型无需改协议。
type Event struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// 预定义事件类型常量。与 Python Agent 侧约定一致，
// 网关只做透传，不解释 data 的具体结构。
//
// 事件词表（除下列两个常量外均只作为字符串透传，网关不构造也不判断）：
// token / tool_start / tool_end / card / table / sources / ping / done。
// 与前端约定的完整清单见 agent/app/main.py 顶部注释。
const (
	EventStatus = "status" // 运行状态提示（如「正在思考…」）
	EventError  = "error"  // 错误（结构化，绝不断连）
)

// ErrorData 错误事件的数据载荷。
// code 供前端做分支处理（限流 / 配额 / 上游故障），msg 供展示。
type ErrorData struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// RunRequest 客户端发起一次运行请求的入参。
// WS 场景下 session_id 可省略（回退到握手参数），SSE 场景下随请求体传入。
type RunRequest struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
}
