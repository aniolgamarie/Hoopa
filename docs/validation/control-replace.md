# T136 完整替换、身份拒绝与回滚失败

本文保留0015阶段的结果与当时状态。后续0016已完成[回滚修复和五场有限控制验收](control-rebind.md)，T136本范围完成；本页旧失败仍为失败，不回填结果。

2026-10-25。[接管原型](control-probe.md)之后新增三场隔离 VM：完整替换和身份冲突拒绝通过，端口冲突后的回滚失败。T136 保持未完成，不能把状态机实现、来源完整或 VM 退出 0 当作回滚通过。机器记录（本地原件 `../../experiments/mihomo-events/control-replace-validation.json`，不随Git上传）分别保存成功与失败。

## 固定输入及准备

仍使用 0015 核心 `1ef1b53ae41435596844c264ae5e1e5201ce95b5d2762be872371023f4d6af25` 与 control helper `6574418f4d08ebabd5812ed7b578d14a1a3004eee3dff0a8aeb7c109de86a680`。新镜像 48,598,014 B，SHA256 `dc898a08a2d980558de0fd351d0e0ae1e769b73de250ac1beb42bc5b9b5c9616`；固定 Linux amd64、2 CPU/1 GiB、无网卡和宿主共享，单次最多 150 秒。

原准备 64 项检查后，主代理补齐进程消失记录与确切实例的绑定，以及拒绝、续传、凭据恢复、显式 Recover 的原始单调时间顺序。最终 72 项离线检查通过、0 skip，两个数量不相加。原检查脚本括号语法错误、修正前脚本与两次 freeze 分开保存；镜像和运行使用新的实际脚本 pin。核心与 helper 未改动。

## 实际结果

| 场景 | 结果与边界 | 字节与来源 |
| --- | --- | --- |
| `vm-replace-oeh255uu` | VM/提取/checker 均 0；旧核心 PID 89/start 427 结束，新核心 PID 105/start 602 使用新 generation/epoch；rev1→2，最后显式停止到 rev3 | 旧流 node:a 每方向 11 B、新流 node:b 每方向 17 B；两完整来源，共 14 帧、37 导出 |
| `vm-identity-conflict-fv355ksm` | 三阶段均 0；仅将 claim.start_ticks 加 1 后，status 和 stop 均返回 503/UNKNOWN；原核心继续转发。恢复原始 claim 字节后，显式 Recover 停止到 rev2 | 同一流先 11 B、拒绝停止后再 13 B，终末每方向 24 B；同一完整来源，7 帧、29 导出 |
| `vm-rollback-7y2qfj4a` | VM/提取为 0、checker 为 1；夹具预先占用目标端口；实际新目标和恢复旧 bundle 的实例均输出通用受保护配置拒绝，原运行未采 bind errno，具体因果尚待诊断；操作为 degraded/rolling-back，HTTP 503 | 原流每方向 11 B；原来源 7 帧，失败目标和失败回滚各 1 帧，均为完整 epoch；后两者零应用流。这 9 帧不计入成功控制矩阵 |

完整替换时，288 次只读 `/proc/net/tcp` 采样覆盖约 1.59 秒；保存的状态依次为旧入口监听、两个入口均不监听、新入口监听，未采到重叠。它是有限采样，与状态机“已核验旧 pidfd 退出后才 Start”的顺序共同构成证据，不宣称持续观测。旧应用实际读到 EOF/reset，新应用是加入新 leaf 的新实例，旧连接没有自动迁移。

身份冲突场景的 active 原字节在拒绝停止后不变，只有一个 launch；原 PID/start/generation/epoch 保持，续传发生在夹具恢复 claim 之前。恢复精确原字节是显式故障清理，不能描述为 status 自动修复。另一个 UID/GID 65534、无有效/许可 capabilities 的进程在 0700 实例目录边界得到 EACCES；这证明文件权限拒绝，不是实际执行了 SO_PEERCRED 不匹配分支。

这三场的核心子进程由管理器持有。guest 记录原 PID 消失，未对核心调用 waitpid，也未观察其退出码；管理器正常结束的退出码与核心退出码分开。两次成功合计 3 流、21 帧、66 导出，独立 scout 重算了导出文件和归档成员摘要。

## 回滚失败的定位状态

失败目标和回滚实例均保存了独立 intent/claim、generation 与来源 epoch，stderr 都只有通用 `Hoopa protected configuration rejected`。只读严格解码及恢复核对确认来源完整、没有凭空增加应用字节；这不能证明旧配置已恢复。旧 active 记录保留也不等于旧核心仍然运行。

固定源码显示 `Reserve` 未设置 `SO_REUSEADDR`。固定内核的 bind 冲突逻辑表明，旧监听 socket 关闭后，仍保留的 accepted socket 可能阻止同端口立即重绑；accepted socket 会继承监听 socket 的复用设置。原失败夹具只采集 LISTEN，未记录 bind errno 或非监听 TCP 状态，因此该机制目前只是有源码支持的原因假设。

绑定 errno 的独立诊断尚未实际完成。后续诊断 VM 将在三份可信启动记录对应的实例均已退出之后，保存完整 TCP 状态与原端口 bind 结果，并用自有 socket 对照复用设置。诊断结果不计为 T136 通过；是否修改独立核心候选及重验由主代理依据实际结果裁决。原失败 VM、0015 制品及既有 T137 记录保持原样。

主代理负责故障决策和实际 VM；executor（gpt-6.1-sol/medium）准备独立夹具，scout（gpt-6-luna/high）复核稳定材料及原始运行，无再派生。当前仍为 139 项、完成 23 项，正式 G01 新样本 0、旧 0/100 保留；真实 SSH/注销/休眠、其他内核/平台及性能预算未由本批验证。
