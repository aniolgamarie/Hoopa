# 基础完整替换 v1：T030/T031 接线决定

2026-10-27，root 决定。任务完成与验收范围见任务表及文末链接；契约本身不是验收证据。

## 材料与接纳

`BasicMaterialSource` 是内部可信的只读编译/固定 fixture 适配器，不能由请求选择实现。
它提供已经独立生成预期快照的完整原件；通用 Intent 编译仍由 T037–T041 实现。
材料桥核对 Plan、manifest、全部原件与固定 basic capability，不把哈希完整性当成
完整规则语义校验。provider/rule_asset/app_binding input ID 精确等于原件文件名，无隐式扩展名映射。
core_binary 与 capabilities 各恰一项；名字可为合法 ID，摘要必须对应批准的固定 build。
validation/route_map 附加原件仅核对完整性；固定 controller-snapshot.json 另做 wire 校验。

编译器提供已经存在的私有候选 workdir/config.json；apply 不从环境变量或请求自由
参数生成核心 argv。该目录不能复用任何已登记运行的 workdir。当前启动器以独占创建
launcher.log 保留单次执行证据，回滚必须准备新目录，不能删除旧日志后重启。

公开 BasicApplier 使用 Coordinator 的同一控制锁，先查已有请求，再验证新请求及
独立输入/影响，之后才读取和核对候选。首次请求还须只读预检操作容量/staging。
不可变候选可在 prepared 前发布；此时崩溃只有未接纳候选，不存在可恢复的操作。
所有冲突、确认、材料和容量拒绝先于候选发布。后续持久化或熵失败可留下候选，不能
称为零写校验失败，也不授权启动。不会把已接纳请求的重试当作继续执行命令。

## 运行与发布顺序

初始 prepared 沿用 T027/T029 的空步骤证据；此时尚无进程效果或本操作的 RunIntent，
只持久保留原请求、base/target revision、旧/新 generation、Plan 与 impact 摘要。
在此中断，恢复须用仍匹配的当前 active、不可变原件和原请求重新独立核验；无法证明
则 degraded，不得直接启动。从 validated 起，每一步保留完整 base pointer、旧运行
ID、新运行 ID、目标 manifest 摘要和旧实例退出观察。先 validated，再 applying/stop-old；只有新鲜核验原实例并观察原 pidfd 退出
后才准备目标。prepare-target 先于生成持久 RunIntent，spawn-target 先于消费一次性
启动许可。两次记录之间中断，RunIntent 仍以原 operation/generation 可发现；恢复绝不
重建旧许可，也不能把没有 birth 当成未启动。

新核心关闭入口启动。verifying 阶段独立核对完整预期快照并持久注册后才记录开放意图，
发送一次 activate，重新证明同一实例和 open。之后依次发布 publishing、active、再次
观察运行、committed。文件与核心不构成共同事务。运行失败若已在当前执行中证明旧
实例退出，则尝试下述完整回滚；无法确认则保留 degraded。任何持久写失败立即停止
后续效果，保持原 pending/uncertain 证据供显式恢复。

active.OperationID 明确定义为最近一次**实际核验后的指针发布操作**，不必是最早配置
提交操作。成功回滚保留旧 revision/generation/manifest，但将 OperationID 指向当前
回滚操作，以便关联新的旧配置运行实例。当前实例必须能由 operation + generation +
manifest 找到恰一个 RunIntent，再做实际身份核验；多于一个、缺失或不匹配都拒绝。
同一操作的目标与回滚 generation 不同。此决定细化既有“恢复旧指针”的运行身份含义，
不会修改旧配置内容或把回滚伪装成新 revision。

当前首启只接受 pristine stopped、无 operation ID、无运行及操作历史；stopped-with-history 和自动重启由
T032 补充显式停止证明。第一版成功路径不宣称已经支持任意历史恢复。对恢复待定的
实例，status/doctor 继续只读。

## 自动完整回滚

只有同一次 Apply 已通过原 pidfd 观察旧实例退出，才进入自动回滚。先记录
rolling_back/stop-candidate；候选已有本操作 closed 原始登记，且完整配置、实际
身份和对应激活回执可核验时，才允许通过原 pidfd TERM/WaitExited。closed/open/degraded
各有严格回执谓词，draining 不可由此接管；普通 Observe/Terminate 的限制不变。
未调用过 start 的当前内存流程可记录“没有目标启动”，该结论不适用于重开后缺 birth。

2026-10-29 T051 修正：应用端口在 closed 控制接口启动前 reserve，已占用端口可使
本次候选在登记之前自行退出。此时仅同一次 live Apply 保留的 `LaunchChild` 可提供
替代退出证据：唯一 `cmd.Wait` 已返回真实终末 OS ProcessState（正常退出或信号退出），
原直接子进程 PID、私有启动意图及其原始 SHA 与不可变 RunIntent/RunBirth 完整匹配。
缺 birth、变更的原件、context 取消、非终末或 wait 故障、重建零值句柄均拒绝；不能用
PID 消失或任意错误代替退出。该分支只在本操作 registration 确实不存在且没有持有
已注册 OwnedRun 时使用，不创建登记、不发送信号、不授予接管或重启权限。

退出证明以可选 `target_exit` 保存在原 apply evidence 中：version=1、
kind=direct_child_wait、run_id、operation_id、intent_sha256、birth_sha256、pid、
wait_status。与 target_stopped=true 一起，在准备回滚目录的原有 journal 步骤持久化，
成功后才创建目录或启动旧配置。缺少该可选字段的历史 v1 evidence 字节不变。
写入之前崩溃不恢复内存 Wait 事实，仍为 degraded。已有 closed registration 的路径
继续使用原完整身份/回执/pidfd 停止证明，不能借新分支绕过损坏或缺失的登记。
即使退出证明已持久化，若回滚目录/RunID 尚未创建或回滚实例尚未完整开放时管理器崩溃，
显式恢复仍保持 degraded；退出事实不授权重构启动许可，也不把尚未完成的回滚补启动。

候选确定退出后，先持久记录随机 launch-48hex 目录名，再在 Store 的 runtime root
下独占创建 0700 目录、0600 config.json，依次同步文件/目录/父目录。使用完整旧
immutable bundle、固定 binary 和新启动身份；不复用旧目录、不删除 launcher.log。
创建后发生不确定错误保留部分目录并 poison writer；恢复不把可见目录当成新启动票据。

回滚逐步保存新 RunIntent、新 RunID、闭态核验、开放意图和完整开放核验。active
只能从原 base 或本操作完整 target 转成旧 revision/generation/manifest + 当前 operation。
再次核验相同实例后才 rolled_back；任何步骤无法证明都 degraded，不递归尝试更多实例。
API 失败响应遵循 Response：ok:false、data:null，error.code 为 APPLY_FAILED 或
RECOVERY_REQUIRED，details.operation_id 与 reason 供查询原操作；内部 RunID 保留于
持久 evidence，不添加到 MutationReceipt。

## 显式恢复的有限完成路径

operation.recover 使用独立 request_id、operation_id 与 expected_revision。先重放已
接受身份；新请求在同一控制锁下验证当前 revision、原目标和全部操作记录，再显式
维护严格命名的未发布 operation 临时文件。损坏权威记录、无关未完成操作、未知文件
或不安全元数据均拒绝；维护不进入查询或同请求重放。

恢复只完成已经开放的准确目标/回滚实例。重新核验 immutable run/birth/registration、
原 pidfd 连接所属、完整 snapshot 和精确 activate 回执；目标、回滚、旧退出事实与
active 必须一致。补发布前后都观察相同实例。此路径不 spawn、activate、TERM 或重建
一次性票据；closed、身份未知、缺原件、没有持久退出事实等窗口保持 degraded。
尚未完成的 rollback 不得重新选择 target 作为成功提交。

对于持久 `target_exit`，显式恢复核对精确 typed proof、RunIntent/RunBirth 原件 SHA、
原操作及候选身份，并要求本操作 registration 仍不存在；旧实例的 open 登记和已开放
回滚实例的完整证明保持原要求。恢复不重新 Wait/推断退出，也不补建登记或重新启动。
新证明只覆盖固定核心同 PID 启动链的这个直接子进程，不扩展到任意后代进程审计。

恢复调用先独立持久到 applying，才推进原目标；原目标实际核验终态后，分别完成
本次和此前准确关联到同一原目标的未完成恢复调用。关联包括完整原请求摘要和
目标身份，不更换原目标 operation/request/Intent。两份记录之间可中断：同 ID 仅
返回已存结果；新 ID 的显式恢复重新核验，再补缺失终态。无法确认时原件保留，
禁止自动新写；无关记录不得借恢复跳过。终态正文已压缩的目标暂不接受新恢复调用。

当前实现与受限验收见[自动回滚记录](../../../docs/validation/p1-basic-rollback.md)；
[显式恢复的实际组合](../../../docs/validation/p1-explicit-recovery.md)和
[实际管理器 SIGKILL 补验](../../../docs/validation/p1-manager-crash-recovery.md)分别记录，
不借用自动回滚通过结果。完整产品矩阵仍属 T034。
