# T024：P1 文件存储有限验收

2026-10-27。实现范围是 `internal/config` 的路径和 Linux 文件存储，不含生产
CLI、进程接管、配置编译器或操作恢复。采用[ADR012](../adr/012-p1-file-store.md)
及[文件存储 v1](../../specs/001-hoopa-proxy-manager/contracts/file-store-v1.md)。

## 实现行为

`ResolvePaths` 纯计算 XDG config/state/cache/runtime 路径；SSH 没有 runtime 变量时
回退到 `state/runtime`。拒绝相对、未 clean、NUL 和职责重叠路径，runtime 也不得
占用 active 指针或持久锁的位置。新目录 0700、文件 0600，已有不安全对象只拒绝。

显式 writer 获取 state 持久锁，只有全新状态才能初始化 stopped/null generation；
读取则不创建任何资源。bundle 验证清单及全部文件后不可变发布，active 原子替换
只引用完整 bundle。失败不会伪造旧态：发布结果不确定后停止该实例的后续写入，
重开及控制层核对是独立动作。上层还必须验证语义/能力及实际运行，存储成功不能
被直接解释为代理启动成功。

## 运行证据

- `attempt1`：沙箱中 `/tmp` owner 映射为 65534，实际存储测试被权限前提拒绝，退出 1。
  保留原日志，不把未执行到的存储断言计为通过。
- `attempt2-host`：真实宿主 uid 1002，`/tmp` 为 root/01777；`go test -race ./internal/config`
  退出 0，20 顶层/107 子测试节点、0 失败/0 跳过。其中 5 个顶层属于原有标量类型，
  新存储/路径为 15 顶层/107 子测试节点。
- 最后路径补充：加入 `state/active.json`、`state/.writer.lock` 两个保留位置，定向
  `TestPathsValidate` race 退出 0，1 顶层/53 子测试节点，其中 4 个为本次新增。
  原整包结果保持旧 source pin，新修改仅由新定向结果覆盖。
- 有效实际行为包括独立子进程持锁、删除测试 runtime 后第二 writer 仍被拒绝、
  持锁子进程 SIGKILL 并 wait 后新 writer 成功打开；所有路径仅指向测试自己的目录/进程。
  未发信号给真实 Mihomo 或其他外部进程。

检查还覆盖完整 bundle 幂等/碰撞/损坏、缺文件/多余文件、特殊文件、symlink/hardlink、
权限、尺寸和路径上限、active 引用、逐个写入/同步/发布故障及不确定后拒绝复用。
只读路径比较文件集合、mode、大小、mtime 和内容摘要；它不承诺文件系统不更新 atime。

全部公开源码范围的 gofmt、vet、普通测试及 race 均通过；后两项各 303 顶层/1261
子测试节点，19 个 opt-in 实验测试未运行并明确跳过，另有 2 个无测试包。原 `./...`
发现 private 内冻结工具链而失败的日志保留；显式公开包范围覆盖当前全部四个源码根，
未删除原件或为旧工具链补依赖。

稳定源码审查与最后路径窄审均无阻断，原件及规定检查见
机器记录（本地原件 `../../experiments/gost-mihomo/p1-file-store-validation.json`，不随Git上传）。未通过重复测试
扩大旧运行范围，也没有用编译或只读源码审查替代实际产品生命周期。

## 尚未验证

不含断电、磁盘硬件故障、所有文件系统、同 UID 敌对进程/挂载替换或非 Linux 平台。
打开时的目录同步失败尚无专门故障注入；错误传播及锁关闭经源码核对。
操作日志、运行登记的产品格式和恢复顺序属于 T025–T031，实际核心及会话行为属于
T034。T024 完成不等于 US5、P0 整体或 P1 产品完成。
