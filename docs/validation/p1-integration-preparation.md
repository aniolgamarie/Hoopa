# T034：真实 CLI 传输与恢复前置核验准备

2026-10-27。T033 已完成有限服务/CLI交付；本页记录继续实施 T034 时实际完成的两段
工作。`newProductHarness` 仍明确失败，完整产品矩阵没有被勾选或替换为 mock。
任务仍为 **47/140**。结果索引见准备记录（本地原件 `../../experiments/gost-mihomo/p1-integration-preparation-validation.json`，不随Git上传）。

## 真实 CLI 子进程传输

`tests/integration/p1_cli_transport_test.go` 只在 Linux/amd64 + p1integration 测试中编译。
它使用调用方固定的可执行程序和独立 HOME/XDG，原始 stdin 与 literal argv，验证
一个匹配请求身份的 JSON 响应及退出码，再保存 0600 原件和摘要。有效业务拒绝与
进程/格式/超限/取消分开；不从响应推造进程、节点或生命周期证据。

stdout 最多保留 65,537 字节，包含 65,536 字节正文及 CLI 的最终 LF；先检查原始
长度，再做 canonical/信封校验，保存原字节。stderr 最多私有保留 4 KiB，错误文字
不泄露它。调用最长 20 秒或更早的调用方 deadline；超限/取消只终止自己新建的直接
子进程，WaitDelay 限制异常继承管道。不会杀进程组或核心，也不自动重试。

定向 6 顶层/20 子项 **26 pass，0 fail/skip**，race 同样通过，分别 21.260 / 36.858 s；
tagged vet 通过。驱动子进程明确标记 driver_fixture，不能当成产品运行原件。
其中真实构建的 CLI 在空私有目录执行 status：返回 READ_UNAVAILABLE / exit 4，
精确 176 字节，两份原件 SHA 相同，四个状态目录仍不存在。实际 CLI 是普通构建，
race 覆盖测试驱动及其驱动子进程，不能混称实际 CLI 也已加 race。

70 项原执行 pin 已核对。随后恢复代码修订后，将该阶段源码按原 SHA 封存；其中
recover.go 从 T033 执行前归档精确重建并匹配旧 SHA，明确不是此次事前复制，其余
文件从未变字节复制。该只读运输结果不改标为后来的恢复实现结果。

## 发现并修复的 F14 接纳顺序问题

源码追踪发现：对已终结且本应仍在运行的目标，旧流程先维护 staging 并接纳恢复
调用，再验证进程归属。因此在请求之前就已存在归属冲突时，会新增 degraded 调用，
与 F14 的完整零变化要求不一致。新增回归首先实际复现四个拒绝场景均 made=true，
失败回执保留，没有弱化原断言。

[修订契约](../../specs/001-hoopa-proxy-manager/contracts/recovery-preflight-v1.md)将严格
可解码的终态 config.apply 和 committed runtime.start/adoption 核验移到维护/接纳前。
预先存在冲突时零写拒绝；接纳后仍重复原核验。若冲突随后才出现，保留已经接纳的
pending/degraded 记录。pending、损坏证据、历史 stop 及原请求重放的语义保持明确。

7 个定向顶层用例通过，race 8.251 s、vet 通过，17 项原阶段 pin 稳定；包含临时
staging 也不能被提前清理、原请求重放不核验、接纳后身份变化，以及 rolled-back
start 不误入活核预检。这里的 verifier 是合成值，不能替代实际核心证明。

## 实际原件缺失补验

`recovery-preflight-actual-03` 使用与 T033 相同固定 basic 核心，独立完整单入口 fixture
预先 Apply，再公开 Stop/Start 创建普通 runtime.start 终态。两个核心 PID
**1439477 / 1440009**；4 次授权本地 HTTP echo，材料 Source 一次。

任务仅临时移走这个自有运行的原注册文件。公开 Recover 返回 CONFLICT、made=false，
完整文件树/active 不变，未创建恢复记录，原核心仍正常转发。恢复任务保存的精确
原件后，同一未接纳请求通过全部证明并提交；原终态记录摘要、核心身份和 active
不变。通过原 pidfd 清理；实际补验没有 race，无 VM/BPF。

最终实际运行 2 个顶层 pass＝1 个实际场景＋1 个空 launcher 父分派，0 fail/skip，
14.29 s，执行输入未变。actual-01 的测试变量重名编译失败及 actual-02 的正向结果
但并行源码变更均保留；最终 actual-03 单独运行，不覆盖旧记录。

## 最终检查与剩余工作

新生产逻辑首次受影响包回归：CLI 55 pass、service 102 pass；control 505 pass、
2 fail、1 skip。两项 fail 是同一个旧断言及父测试：目标已终结后，新预检使证明次数
从 2 增至 4，而旧断言仍要求 2。仅修订该分支为“接纳前两次＋接纳后两次”，保留
所有结果、原记录和未终结分支断言；没有放宽证明。

随后只复验发生测试修订的 control：**507 pass、0 fail、1 skip**，79 顶层/429 子项，
73.13 s，输入未变。唯一 skip 仍为未传可选核心变量的已有测试；实际核心另列。
最终 control/service/cli/cmd vet 退出 0。不同范围/版本的两次回归不伪装成一次全套
通过，也不与定向检查相加。T033 原冻结结果按其旧版本保留。

已完成：上述运输 seam、恢复顺序修订及受限补验。进行中：完整双入口固定 fixture
转换、真实产品 adapter、实际调用/信号/故障原件采集。失败待决策：本次已发现的
接纳顺序问题已解决，无新增未裁决项。环境不足未验证：T034 实际 SSH/logout/休眠
及其他平台；不能用信号模拟或移植旧制品结果。

完整 adapter 还必须区分 pristine 与已有停止快照的准备、用独立完整 fixture 调用
真实 BasicApplier、将恢复请求绑定原持久操作 ID、记录保留连接的实际节点与字节。
通用编译器仍是后续任务，不能作为这部分缺少用户材料的借口，也不能塞进生产假源。
root 负责恢复顺序与验收；executor 实际 gpt-6.1-sol / medium，scout 实际 gpt-6-luna /
high，独立复核未重复执行测试，没有再派生。T034 与整个 P1 仍未完成。
