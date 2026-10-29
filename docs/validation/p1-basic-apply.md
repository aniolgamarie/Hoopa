# T030 基础首启与完整替换：已验部分

2026-10-27。公开 `BasicApplier.Apply` 已接入实际固定 Mihomo 核心，完成一次首启和
同端口完整替换。本文保留当时的首启/替换验收版本；后续[自动完整回滚](p1-basic-rollback.md)
已通过独立实际场景，[显式恢复](p1-explicit-recovery.md)也有独立有限证据，历史 stopped 状态由 T032 实现。
本记录不表示产品生命周期或整个 P1 完成。

代码在 [apply.go](../../internal/control/apply.go)、
[basic_runtime_linux.go](../../internal/control/basic_runtime_linux.go) 和
[basic_material_linux.go](../../internal/control/basic_material_linux.go)。
机器索引为 p1-basic-apply-validation.json（本地原件 `../../experiments/gost-mihomo/p1-basic-apply-validation.json`，不随Git上传），
顺序与边界见[基础替换契约](../../specs/001-hoopa-proxy-manager/contracts/basic-apply-v1.md)。

## 实际核心证据

actual-01 运行真实已批准 build `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`，
从公开 Apply 首启 PID 1883176，再完整替换为 PID 1883228。两次使用独立 generation、
workdir 和 RunID，接替相同 controller 及应用端口。替换前、后各完成一次有凭据的
本地 HTTP echo；无凭据请求返回 407 且没有到达目标。旧实例退出由预先保留并完整
核验的原 pidfd 观察，另观察 /proc 已不存在。

同一 apply 请求再次提交时，改变后的外部编译条件和 nil context 均未触发编译或
重复启动；运行记录仍为 2 项，状态树原件、权限和时间未改变。最后仅清理测试所有的
强身份实例，原 pidfd TERM/退出与 /proc 消失通过。内部 LaunchChild.Wait 的退出码
没有暴露给测试，不声称取得独立 child Wait 证据。目标 launcher.log 实际为空；旧
核心退出后未单独采集其日志，这是归档限制。

这一专项是 1 个实际场景通过，另有 1 个空 launcher dispatch helper；0 fail/skip。
它没有执行 VM/BPF，也不测试公网节点质量。固定 fixture 适配器独立绑定原件，不是
通用 Intent 编译器。单入口实际通过与既有完整快照 inspector 的有限证据分别记录，
多应用完整故障矩阵仍在 T034。

## 存储与顺序回归

最终受影响包 race：control 35 顶层、236 子测试，共 271 pass；config 53 顶层、356
子测试，共 409 pass；总计 680 pass，0 fail/skip。命令使用 `-skip ^TestBasicApplyCore`
排除已经单独运行的实际核心专项及空 dispatcher，不把排除项计作本次通过。材料桥的
实际固定 ELF 只读校验仍运行。两包 vet、gofmt 和空白检查通过，输入原件执行前后
一致。no-index 检查新文件的退出码 1 表示存在差异，原输出无空白诊断。

合成运行后端结合真实 Store/journal 验证阶段顺序、每个效果之前的持久步骤、13 个
中断位置、运行错误保留 degraded、写入者关闭后停止动作、活动运行唯一关联及压缩
日志后关联保留、歧义拒绝、首启历史约束。这些测试不能代替真实进程故障证据。
操作预检另外覆盖空库、只读、staging、损坏、实际 1,024 项容量、父目录同步失败后的
poisoned writer 和关闭句柄，且全部只读。

审查明确了两个边界：prepared 的初始步骤仍为空证据，未产生进程效果，恢复必须
重新独立核验；首启必须没有 operation ID、运行及操作历史。候选已发布但尚未接纳
的中断不能当作已存在操作去恢复。没有把这些窗口藏成跨文件或跨进程原子事务。

主代理实现控制状态机、运行适配及关键不变量，executor（gpt-6.1-sol / medium）实现
材料桥和实际测试，scout（gpt-6-luna / high）独立审查调用顺序。材料桥专项 44 pass
仅证明材料绑定；其首次编译因并行接口尚未落盘而未执行测试，历史诊断原样保留。

## 剩余工作

后续已完成候选失败后的可靠终止、完整旧 bundle 回滚及新回滚工作目录的有限验收，
见[独立记录](p1-basic-rollback.md)。指针与实际运行的显式消歧、独立恢复请求去重和
持久 staging 维护另行实现与验证。特别是已丢失原 pidfd 的旧
进程，缺失 birth/Observe 失败不能解释为停止。T032 再补显式 stopped 历史与受控
重启，T033 接生产 CLI，T034 执行 F10–F14 等真实完整故障矩阵。
