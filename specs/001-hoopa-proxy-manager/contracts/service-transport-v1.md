# 本地管理服务传输 v1

2026-10-27。T033 的传输实现约束；本文件不表示服务、CLI 或 T034 已交付。
业务操作仍以 [CLI/JSON v1](cli.md)、[API Schema](api-v1.schema.json) 和
[运行生命周期](runtime-lifecycle-v1.md)为准。

## 连接与帧

传输函数只接受已经由平台层完成私有目录、socket 权限和实际 SO_PEERCRED 核验的
Unix 连接。它不自行发现、监听、认证、启动服务或取得 writer。连接认证不等于核心
运行身份验证；后者仍由 control/runtime 完成。

每个连接恰好一次请求、一次响应。每个方向都是四字节无符号大端长度、该长度的
UTF-8 JSON 正文，然后发送方 `CloseWrite`。接收方要求正文后立即 EOF；零长度、
超限、截断、尾随字节、第二帧均拒绝。请求原始正文上限 128 KiB，响应 64 KiB，
在分配正文及 canonical 解析之前检查长度；不能靠去除空白后变短绕过上限。

请求使用现有严格请求信封解析器；响应使用 `Response/Error/Warning` 的精确信封
形状，成功时 request_id 非 null、data 为对象、error 为 null，失败时反之。
错误代码和 details、warning 字段遵循 Schema。Exchange 要求响应 request_id 与
原请求一致。此处只验证信封，操作专属 params/data 仍须由业务层另行验证。

允许合法空白和 key 顺序。通过 CanonicalJSON 拒绝重复 key、无效 Unicode、尾随
JSON 和非法数字后校验形状，但收发保留原始字节，以便精确重放已持久 Result。
无效本地请求/响应在发送帧及 CloseWrite 之前拒绝，不输出 payload 到错误信息。

## 资源与取消

每个调用使用一次绝对 I/O deadline：调用开始后最多 15 秒，或调用方更早的 context
deadline；不按每个字节延长。context 取消中断阻塞读写，必要时关闭本次调用专有
的连接。取消回调必须完成后才返回，避免返回后的回调影响调用方接续处理。

调用方不得让其他 goroutine 同时使用该连接，也不能在取消后复用。成功路径不关闭
连接的读半部，由调用方最终 Close；每连接一次交换的约束使请求的结果不明时不会
在同连接隐式重试。服务不得把传输失败解释为尚未接纳操作；客户端须使用同 ID 查询
或重放，不能自动生成新 ID 重做变更。

## 责任分配

- `internal/service/wire_linux.go` 提供 ReadRequest、WriteResponse、Exchange；
  不访问文件存储、不运行核心、不改变去重或控制状态。
- 平台 IPC 层负责连接和 peer；服务路由层负责版本、argv/operation 绑定、精确参数、
  业务错误映射及一次请求的控制调用。
- 服务退出、端点发现、启动就绪、旧服务 stop 重放，以及核心接管/重启授权，需要
  后续服务层单独实现和实际验收。一个往返成功不能当作这些行为已经通过。
