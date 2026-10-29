# P1 文件存储 v1：T024 实现边界

2026-10-27，root 实现决定。依据 data-model 第 1–3 节与 T018；本文定义即将
实现的文件接口，不代表 T024 或生产生命周期已验收。不含 SQLite。

## 路径与创建

| 用途 | 默认位置 | 内容 |
| --- | --- | --- |
| config | `$XDG_CONFIG_HOME/hoopa`，未设置时 `$HOME/.config/hoopa` | 作者草稿 |
| state | `$XDG_STATE_HOME/hoopa`，未设置时 `$HOME/.local/state/hoopa` | `active.json`、`bundles/`、`operations/`、`runs/`、`credentials/`；T054/T056计划增加独立写权的`app-launches/` |
| cache | `$XDG_CACHE_HOME/hoopa`，未设置时 `$HOME/.cache/hoopa` | 可丢弃下载 |
| runtime | `$XDG_RUNTIME_DIR/hoopa`；未设置时 `state/runtime` | 私有 socket、临时运行资源 |

路径解析是纯计算；非空相对 XDG 路径返回输入错误，不默默改用其他位置。
config/state/cache 不允许相互包含；runtime 可以位于 state 内，不能包含持久目录。
没有 XDG runtime 的 SSH 会话可使用上述回退，不创建系统服务或计划任务。
目录清理不能以 runtime 的父目录为删除范围。XDG 输入须已 clean，runtime 不得与
state 的 bundles/operations/runs/credentials 子树、active.json 或 .writer.lock 相交。

只有显式初始化/写打开可以创建目录。只读打开缺状态时返回不可读错误，不创建
锁、目录、active pointer 或默认配置。新私有目录 0700、文件 0600；已有不安全
权限、错误 owner、符号链接/特殊文件拒绝，不在查询或打开时顺手 chmod/修复。
Linux 逐组件 openat/O_NOFOLLOW；祖先只接受当前 uid 或 root 控制，拒绝组/其他用户
可写祖先（root-owned sticky 目录例外）。不安全环境返回错误，不放宽到任意 UID。
同 UID 恶意进程及挂载替换超出本存储隔离范围。Linux 首先实现并验收；
其他平台需要自己的权限/锁/同步验证。

## 权威与写入

单个实例的配置/核心状态文件写入者持有 state 中的 `.writer.lock`（Linux flock，非阻塞）。
锁文件持久存在；持有它的进程退出后内核释放锁。不能因可清空的 runtime 目录
被删而创建第二个持久状态写入者。它与后续 T025 的管理 IPC/进程身份是不同边界。

T054/T056 拟增加的应用生命周期历史严格限定在 `state/app-launches/`，采用[应用 launch 存储契约](application-launch-store-v1.md)规定的分区写权：init 只在 Store writer 内安装身份，单个 wrapper 只追加其新建目录，不能写配置/核心状态、其他 launch 或重放历史执行。该例外尚未实现/验收；既有 Store writer 本身不获得重写/回收 launch 历史的权限，普通 Store 打开不安装或修复该空间。
服务内运行变更仍由 T029 串行协调；存储锁不证明核心归属。

初始化创建 stopped、无 generation 的 active pointer，revision 使用新的随机提交
token，operation_id 为 null。只有全新状态可初始化；已有持久子目录却缺少
active.json，即使子目录为空，也拒绝生成默认权威状态，等待显式恢复。后续 revision 由控制层生成；配置快照 generation、
核心 RunID 和 CounterEpoch 分开。只读打开不初始化这个记录。

bundle 使用版本 1 manifest：generation、compiler_version、capabilities_digest、
binary 引用（固定 path/version/SHA-256），以及有 kind/name/bytes/SHA-256 的文件清单。
文件类型覆盖 intent、provider、rule、app_binding、generated_config、route_map、validation。
这只是持久完整性封装；T026/T028/T040 仍须验证二进制身份、能力与配置语义。
不会因为文件能被存储便宣称其可以启动代理。

每个 generation 使用 SHA-256(token) 作为目录名，原 token 保留在 manifest。
文件名是有界 ASCII 单个名称，禁止目录穿越；manifest 统一序列化后按原字节摘要。
这个摘要不是 Plan 的 RFC8785 JCS 摘要，后者仍由 T028 实现。
manifest 最多 1 MiB，bundle 最多 256 个文件、合计 64 MiB；active 最多 64 KiB。
超过上限在发布前拒绝，不能部分接纳或截断；未来调大需显式修改、说明和验收。

bundle 在同一 bundles 目录的独占临时目录完成：文件写入/Sync/Close，manifest
写入/Sync/Close，临时目录 Sync，再发布 generation 目录并同步 bundles 父目录。
在文件锁下禁止覆盖已有 generation；相同 manifest 和全部文件可幂等返回，任何
差异或损坏拒绝。读取必须验证 manifest 与每个文件的范围、类型、大小和摘要。
未发布的临时文件不是权威，不因其存在自动恢复或清理。

active pointer 含版本、revision、generation、manifest_sha256、operation_id、
desired_runtime；非空 generation 只能引用已经完整持久化的 bundle。
写入先验证引用，然后在 state 同目录独占临时文件写入/Sync/Close、rename，再
同步 state 目录。操作日志终态仍是另一独立写入，不能称为跨文件/进程共同事务。

发布/目录同步结果不确定时，此 writer 禁止后续写入，必须关闭并在重新打开后
核对原操作与实际状态；不能仅凭内存宣称旧态或新态。显式 writer 重开时
重新同步持久子目录及 state；同一 boot 内关闭重开本身不是持久化屏障。查询仍可读取文件并报告
不确定。恢复动作属于 T031，存储打开本身不补记 committed、不启动或停止核心。

## 有限验收

T024 测试覆盖纯路径解析、只读零创建、私有权限、第二写入者拒绝、完整 bundle
及碰撞/损坏拒绝、active 引用约束、发布前失败与发布后不确定及重开核对。
同目录 rename 和文件/父目录同步顺序分别断言；进程死亡/真实核心恢复由
T022/T023/T034 的产品集成验收完成，文件测试不替代它们。
