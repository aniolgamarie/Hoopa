# 原始准入调用耗时的有限测量 v1

2026-10-24，运行前登记。T132 要记录关联等待；此前资源样本中的 Admit 内部时长为 unavailable，不能拿协议 ready 耗时补值。本批只补实际调用边界的可观察时长，不改变准入算法或宣称分解了锁等待和 BPF syscall 时间。

以固定001–013源码树为输入，用 Go `-overlay` 生成独立私有测量制品：只把原 `Owner.Admit` 方法改名，新增同签名 wrapper，在调用原方法前取 `time.Now()`，返回后立即 `time.Since(start)`。原方法仍覆盖原 mutex 获取/释放、socket 取证、对象/故障/世代核验和map读取；所有身份返回值、错误、状态、引用生命周期保持原样。计时不包含后续测量 JSON 序列化/写 stderr；没有原方法前的应用/协议I/O。原方法的返回前副作用不会因测量输出失败而回滚，也不把输出失败改成准入通过。测量输出不进入来源帧、账本或生产控制API。

每次调用在 stderr 写一条带固定标记的有界 JSON：schema、clock=go_time_monotonic、scope=owner_admit_call_including_mutex_wait、十进制 elapsed_ns、admitted。成功时复制原返回身份的 core/capture generation、accepted/client cookie、原PID/TID/leader启动身份；失败时 connector=null，不用当前writer、错误文本或后续扫描补身份。日志顺序不用于关联，成功记录按原 accepted cookie 唯一匹配同一真实 opened。整段耗时不包含函数调用前的 accept 排队、TCP 握手及内核捕获回调，不能叫纯锁等待、端到端关联延迟或连接ready延迟；不承诺日志持久性或生产采集能力。

只对新测量制品另建全新隔离镜像/运行，保持原0013二进制、源码树、旧guest和所有旧证据的摘要。沿用已验的 family-concurrent 模式：同名父子各自HTTP/node:a与SOCKS5/node:b，两条真实ready都先于首closed。原20FD/v2、raw296/独立exe oracle、六项身份值、同世代与字节终末检查保留；每流33/33、SHUT_WR后完整回显/EOF，13个严格v4帧、完整epoch、核心SIGTERM0/客户端退出回收。两条测量JSON必须各唯一对应opened且 elapsed_ns 为非负规范整数，缺记录/截断/重号/额外记录均失败；实际值必须报告，即使大于预期，不以调高阈值隐藏。运行前不设未经校准的性能通过阈值。

验证分层：overlay仅改变方法名及添加wrapper的diff和源树前后摘要；在实际overlay上运行原coreadmission包race回归，保留原失败/拒绝行为；真实guest日志与原始opened交叉核对。宿主不加载BPF、不改cgroup/sysctl；仍用无网卡/无宿主共享的有界自有VM。本批不重跑已完成的完整生命周期矩阵，也不把两个样本冒充分位数、10分钟预算或新正式G01样本。

各阶段状态分别记录准备/实际完成/失败待决策/环境不足。正式identity/name/path能力、T132/T137与P0/P1门槛不因新增计时而自动提升。
