# T026 管理器控制适配：有限验收

2026-10-27。root 接受 Inspector 与登记后 Activate/Drain 组件的下述有限证据。
离线 launcher 和真实登记→激活组合仍在验证，**T026 尚未完成，P1 尚未交付**。
机器索引：p1-controller-validation.json（本地原件 `../../experiments/gost-mihomo/p1-controller-validation.json`，不随Git上传）。

## 已实现与验证的边界

`Controller` 从不可变 bundle 获取原配置和独立 expected snapshot，核对固定二进制、
本地受信能力声明及摘要。运行层先证明本次 TCP 连接属于原 pidfd 对应的进程，才向
该连接发送凭据。连续 status/snapshot/status 必须匹配完整期待，以最后一次当前状态
决定可用性；不会由核心自己的响应生成期待快照或补齐缺失材料。

`ReadControlRegistration` 仅读取已存在登记，额外要求当前 Store 持写锁且未 poisoned。
写操作通过 `OwnedRun` 再次绑定 intent/birth/登记原件与实际进程，沿同一连接先完整
核验、至多发送一次 POST、再完整核验，最后重验原进程及 argv。不自动重连、重试或
换操作 ID。历史 Receipt 与当前 Current 分开：排空后旧 activate 可返回 opened，
但 Current 仍为 draining。事后核验失败保留已核验回执，Current 为空；未知 ACK 不
意味着没有副作用。已确认的 failed/503 保留对应 degraded 状态并返回独立失败错误。

Store mutex 不跨网络 IO。上层协调器必须串行执行控制操作并保持 writer 生命周期；
当前组件不保证在调用者违反该约定、并发破坏 Store 状态后自动撤销已经发送的操作。
持久 Operation、revision、active 发布与恢复政策仍属 T027–T032。

## 实际执行

- Inspector 默认 race：9 顶层、60 子节点，68 pass/1 明确 core opt-in skip；另一次
  固定核心实际运行 1 顶层通过，无子测试和跳过。vet/format 通过，38 输入另存原字节。
- mutation 受影响 race：15 顶层、84 子节点，98 pass/0 fail/1 既有 core opt-in skip。
  其中两个 pass 是空 fixture 父入口，96 个通过节点有断言。43 输入另存原字节，
  既有 Inspector 的 HTTP 回归一并覆盖；没有重跑此前真实 Mihomo 样本。
- mutation 最终运行实际启动3个自有 Go fixture 子进程，使用真实出生记录、同 PID
  exec、nonce、argv、原 pidfd 与同连接归属证明，均正常 Wait。其 HTTP 控制语义是
  合成夹具，**实际 Mihomo mutation 样本为0**。两个公开方法组合夹具共9个连接、
  49个 HTTP 请求；一次完整操作为同连接3 GET + 1 POST + 3 GET。

Inspector 的单次真实样本使用固定 `2921c246…f27fabb` 核心、事先独立构造的 snapshot，
通过同连接核验观察 closed，两应用端口均拒绝 connect，仅读 Store 树不变，正常
SIGTERM + Wait 退出0。该样本没有 birth/registration/activate/drain，不声称管理器恢复。

## 修复与证据边界

实际 HTTP 测试发现非分块 Trailer 声明和重复 Content-Encoding 可漏检，已改为拒绝
所有相关 header 声明；原失败源码与日志保留。另修复共享 bufio 已预读尾部漏检，
内外两层预读回归都通过。响应头限8 KiB、明确 JSON 正文限64 KiB；拒绝不符协议的
状态、类型、长度、编码、尾部和重定向。普通 HTTP 不证明迟到字节的因果，仍依赖
已证明的连接归属及固定受信核心。context 可中断 socket 阶段，不抢占同步磁盘读取。

mutation 首轮取消测试把40 ms预算放在完整身份核验前，尚未进入 callback 就超时；
修正为进入 callback 后取消，再验证阻塞读和最终原句柄核验。生产代码没有因此放宽。
该轮失败及各自的实际子进程留在原记录，不计入最终3个子进程的分母。

独审分别核对 Inspector 的74冻结条目/38输入、mutation 的180冻结条目/181引用/
43输入。最终审查使用当时源码快照；后续新增 launcher 文件不追溯添加为旧测试输入。
所有 CapabilityRecord 都明确为测试夹具，不是本轮发放的生产能力记录。
无 VM/BPF、实际服务死亡/注销/休眠、完整平台或性能预算结论。当前 Observe 拒绝
不可用的 draining/degraded，未来恢复必须把归属证明与可用性分开，不能推导进程不存在。

executor gpt-6.1-sol/medium 完成测试和常规修正；scout gpt-6-luna/high 独立核对稳定
源码与证据；root 实现控制/进程语义并做最终有限接受。
