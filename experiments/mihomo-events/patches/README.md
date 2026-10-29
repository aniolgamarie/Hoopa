# 固定 Mihomo 实验补丁

`0001-native-observer.patch`针对官方commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`，
共16个改动文件；已在未修改的固定checkout上执行`git apply --check`通过。
实际文件摘要、补丁摘要、工具链、构建参数和二进制摘要见[候选manifest](../candidate-manifest.json)。
Git只保存文本补丁、测试和说明，不保存外部核心二进制、模块缓存或真实日志。

上游源码的GNU GPL version3许可和版权仍适用；新增Hoopa原创trace代码标明MIT来源，组合核心继续遵循上游许可。
不能将本目录补丁上下文或修改核心整体当成Hoopa管理器的MIT实现。此次是本地隔离实验，没有发行修改核心；
未来发行前仍须核对对应完整源码、修改说明、版权、传递依赖及其他适用义务。

只在新的私有工作副本应用，不修改[固定输入缓存](../source-review.md)或用户核心。
构建固定使用Go1.27.3（新增实验代码使用os.Root等API），并非上游go.mod所写最低Go版本的兼容性承诺。
从已应用补丁的工作副本执行，输出路径仍为私有/tmp：

```sh
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-mihomo-events-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go build -p=2 -buildvcs=false -trimpath \
  '-ldflags=-X github.com/metacubex/mihomo/constant.Version=hoopa-p0-fc8c5a2 -X github.com/metacubex/mihomo/constant.BuildTime=2026-10-22' \
  -o /tmp/hoopa-mihomo-events-ildaobd2/mihomo-hoopa-candidate .
```

只有固定go.mod/go.sum已有依赖；本轮下载使用官方Go模块代理及sum.golang.org校验，未关闭校验或新增依赖。
`-buildvcs=false`用于无.git的隔离归档副本，来源由独立commit/补丁摘要记录，不能伪称二进制具有原生干净VCS证明。

候选默认不开启Hoopa观测。显式环境为HOOPA_TRACE_INBOUND、DIR、GENERATION及可选MAX_BYTES、RECEIVER、SECRET，
仅作用对应实验子进程；目录必须预先存在、当前用户所有且0700，文件0600。
RECEIVER仅允许无用户信息/查询参数的loopback HTTP URL且要求独立secret；未配置时只记录本地源帧。
发送器不用父代理，重定向拒绝；secret不写源清单或事件。

回归只执行与来源/钩子相关的断言：

```sh
HOOPA_TRACE_NETWORK_TEST=1 GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache \
GOMODCACHE=/tmp/hoopa-mihomo-events-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -count=1 \
  -run 'Test(Source|Hoopa)' ./component/hoopatrace ./rules/common ./rules/logic ./tunnel/statistic ./tunnel
```

此测试需要明确的回环权限。当前23项通过不代表上游全量测试、源完整性或正式平台验收通过；
严格G01已失败，见[实际验证](../../../docs/validation/native-candidate.md)。

## 追加002：独立生命周期实验

`0002-source-lifecycle.patch`须在同一固定baseline先实际应用001后再应用；它修改/新增6个文件，加入周期checkpoint、独立epoch额度、正常shutdown marker及相关定向测试。全新副本依次apply001/002与旧/新源码整树及权限一致；旧001与候选制品未覆盖。对应[独立manifest](../lifecycle-candidate-manifest.json)与[实际验证](../../../docs/validation/source-lifecycle.md)。

新二进制版本`hoopa-t133-lifecycle`、SHA `aad983b1e6597704fa5de84793da5869d9d6680b43b57c689a8be716292b5655`，不取得T137/CapabilityRecord放行。仅对新的私有工作副本应用，不能在旧实验或用户核心上原位更新。新增ACK丢失/重开测试位于`replay_linux_test.go`，仅测试源码在构建后追加；运行代码未变化，未独立重现二进制构建。

截至002阶段，完整T133仍缺核心重开事件族严格解析、来源恢复gap及lazy-handshake/完整故障验收。管理器接收器和只读reader已严格解析，但不是核心重开实现；两者不可混称完成。

## 追加003：SOCKS5成功响应前提修复

`0003-socks5-reply-validation.patch`在全新私有副本先应用001、002后再应用，修改握手回复头校验并新增纯合成流测试。固定候选此前在读取VER/REP/RSV后直接读地址，未拒绝非成功REP；补丁要求VER=5、RSV=0、REP=0，否则返回错误，避免仅因响应地址可解析就报告握手成功。

修复前27个叶子用例中11失败，修复后27全部通过，race/vet/gofmt及fresh副本实际apply/整树核对通过；完整证据（本地原件 `../socks5-reply-validation.json`，不随Git上传）保留测试前后日志、固定输入和新文件摘要。没有新binary、真实代理/两线路或BPF运行；不能将其算作T134/G03或CapabilityRecord完成，001/002候选的旧hash/结果不变。下一步按[ADR007](../../../docs/adr/007-p0-evidence-closure.md)在协议确认处接ready观测并做真实校准。

## 追加004/005：来源恢复校验与流缓冲边界

[004](0004-source-recovery-validation.patch)仅修改/新增12个`component/hoopatrace/`文件：同源纯v1/manifest/epoch校验接入来源重开、确认写入和sender，保留原帧重放并提供独立派生恢复报告。缺final无writer结束证明保持pending；持锁稳定全扫描才派生未知尾部gap，不新增来源恢复事件或伪造网络终态。33条并发pending拒绝，终结历史不受32条并发上限限制。

[005](0005-stream-buffer-boundaries.patch)仅包含5个HTTP/tunnel文件，修复HTTP CONNECT响应解析的预读payload保留，并将trace lazy首次Write的actual n计量抽取为可注入测试边界。HTTP合成流从2/4子例失败到4/4通过；lazy 8个边界子例通过，不代表协议ready/两线路验收。

两者须在全新私有固定upstream副本依次实际应用001、002、003、004、005；本次五个补丁的check/apply均通过，fresh整树内容和权限与已审查源码一致并实际构建。新版本`hoopa-t133-closure`，binary SHA-256 `bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2`；004 SHA `53b740fb3131f0428ca5d0dcce6331bf2a60d7921ef0fdff69df4bac06458204`，005 SHA `c5f031ce085e201618a4c3cb987a15e555af826452fe8d668faaaa7de9205dd5`。固定Go1.27.3离线构建，go.mod/go.sum不变；旧补丁、manifest与二进制未覆盖。

详细文件/构建摘要见[独立manifest](../closure-candidate-manifest.json)，阶段测试/skip/沙箱失败/显式网络通过分别见机器验证（本地原件 `../closure-validation.json`，不随Git上传）；[本次实际记录](../../../docs/validation/source-recovery-closure.md)保留新binary首次真实组合normal三流通过、active失败、crash未执行及孤立诊断的区别。来源32epoch/8MiB扫描与32pending为独立样本，heap抽样/进程MaxRSS不表示产品闲置预算通过。截至004/005阶段，T132/T133/T134/T137仍未勾选，旧G01的0/100不变，不生成passed CapabilityRecord或宣称P0/G02完成。仅在新副本使用，不原位更新用户核心。

## 追加006：协议ready、实际线路和连接段

[006](0006-quality-ready-route-segments.patch)在001–005之后应用，20个核心文件变化；含显式v2、单调ready与finish分离、DIRECT/HTTP/SOCKS证明、未知外层隔离、实际Conn链及同锁连接段累计。八份共同生产wire文本与Hoopa仓库逐字一致。v2新增不升级ProcessEvidence，v1仍可选且原文可重开；profile未显式选择2时继续v1。

新私有upstream副本实际check/apply001–006后，与已审查源树内容/路径/权限一致，固定Go1.27.3离线构建，go.mod/go.sum未改。006 SHA256 `62d743872d1cad47b98a09e7eff41c5fbd517016df8660c66e4c8acb6437cb98`，最终binary SHA256 `9d52edee280d7e4893911e3c4a6ae43150cf4914421431f7ee19567505459b29`，版本`hoopa-t134-quality-final`。见[完整manifest](../quality-candidate-manifest.json)、机器验收（本地原件 `../quality-validation.json`，不随Git上传）和[范围与历史失败](../../../docs/validation/quality-ready.md)。既有001–005和旧manifest不覆盖，二进制不进入Git。

在已应用六个补丁的私有source目录构建：

```sh
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache \
GOMODCACHE=/tmp/hoopa-mihomo-events-modcache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go build -p=2 -trimpath -buildvcs=false \
  '-ldflags=-X github.com/metacubex/mihomo/constant.Version=hoopa-t134-quality-final -X github.com/metacubex/mihomo/constant.BuildTime=2026-10-23' \
  -o /tmp/hoopa-t134-artifact-ul72y63w/mihomo-quality .
```

复现时使用新的私有输出目录，保留旧候选；路径为本次证据定位，不要求覆盖它。截至006阶段，T134局部完成不等于T137完整候选，当时T132/T135及正式门槛仍未满足；后续007的局部结果另记。

## 追加007：配置能力拒绝与受保护入口

[007](0007-protected-entry-capabilities.patch)在001–006之后应用，加入逐字相同的纯profile校验器、受保护独立启动路径、来源可写/首流容量预校验、全部socket只bind预留后listen、独立入口认证及HTTP逐请求重验、SOCKS5仅TCP CONNECT、只读status/drain控制面。generation/hash固定，SIGHUP不热reload；drain保留旧TCP，完整replace另起独立进程/入口。规则仍按原序首匹配，所有进程规则及不支持组合加载期拒绝，不能以永久拒绝替代身份能力。

新私有固定upstream副本实际check/apply001–007后，与已审查源树内容/路径/权限一致，固定Go1.27.3离线构建。007 SHA256 `74ae6bc4fb6e940fb49d6e91d4b8c5931e1fe4aa9b7a013c2082de1563de79f0`；最终binary版本`hoopa-t135-guard-final`、SHA256 `7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f`，位于`/tmp/hoopa-t135-artifact-tg95ee9o/mihomo-guard`。见[新manifest](../capability-candidate-manifest.json)、机器验证（本地原件 `../capability-validation.json`，不随Git上传）、[契约](../../../specs/001-hoopa-proxy-manager/contracts/protected-core-v1.md)及[分层验收](../../../docs/validation/protected-entry.md)。既有补丁、候选及历史失败全部保留。

从已在新私有副本应用七个补丁的source目录执行；输出使用新目录，绝不覆盖上面的实际制品或旧核心：

```sh
guard_output=$(mktemp -d /tmp/hoopa-t135-rebuild-XXXXXX)
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache \
GOMODCACHE=/tmp/hoopa-mihomo-events-modcache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go build -p=2 -trimpath -buildvcs=false \
  '-ldflags=-X github.com/metacubex/mihomo/constant.Version=hoopa-t135-guard-final -X github.com/metacubex/mihomo/constant.BuildTime=2026-10-23' \
  -o "$guard_output/mihomo-guard" .
```

此命令要求固定模块缓存完整，不下载依赖；新输出须另行核验摘要，不能凭相同版本名继承旧hash结果。实际最终候选的[显式复验命令](../../../specs/001-hoopa-proxy-manager/quickstart.md#t135受保护实验入口与显式复验)已执行：2顶层、含子测试49项pass，0fail/skip，2.839s；harness为race，core为普通构建。T135局部完成，当前139项/21完成；T132身份、T136控制harness、T137完整候选及正式门槛仍未满足，P0/P1未放行。仅Linux/amd64 IPv4 loopback实验，同UID无OS隔离。

## 追加008/009：有限核心准入与半关闭控制

[008](0008-core-connector-admission.patch)在001–007之后应用，加入核心持有的身份资源核验、原 Accept→Admit→不可变 metadata 值→同 flow profile3 opened。`exec_core` 保持同一 PID exec，移交 6 map、3 program、2 link 共 11 个 FD；核心恢复 CLOEXEC。v3 严格验证 connector 及原始 ABI，其他事件保持 v2 质量/分段语义，质量开关同时识别2和3；旧 process unknown、正式 identity 能力 false、PROCESS/UID 加载期拒绝保持。身份只在 durable opened 原帧及解码值，RecoveryFlow 不展示身份。字段与时间坐标见[v3契约](../../../specs/001-hoopa-proxy-manager/contracts/source-connector-v3.md)。

[009](0009-metered-tcp-half-close.patch)在001–008之后应用，单独修复真实 early-half-close 请求暴露的回归：generic tracker 的 Upstream 可由 common.Cast 递归穿过，但 Hoopa 计量 wrapper 为阻止读写绕过而不暴露 Upstream，漏传了 CloseWrite。补丁显式转发该半关闭控制操作，继续隐藏读写 fastpath；不将失败改成允许不收回复的测试。旧失败与修后同请求证据均保留。

最终001–009从固定干净 Mihomo v1.19.26 / `fc8c5a24b16991f98cd736950c17d1aa306a5041`依序check/apply，fresh与目标的路径、原始bytes、symlink及mode完整一致。Go1.27.3离线执行 `go build -p=2 -trimpath -buildvcs=false`，不传ldflags；最终binary SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`。打印的默认 `1.10.0` 不是源码版本，来源由固定commit、补丁和完整树摘要证明，不声称含原生干净VCS信息。实际freshapply/build记录为私有 `artifact-halfclose/apply-build.json`，公开摘要见[新manifest](../core-connector-candidate-manifest.json)。旧001–007、旧manifest、v1/v2失败候选与原始日志不覆盖。

最终候选两协议baseline及原early-half-close均核对双向各33B，旧文件重开hash不变、两世代并存；错generation/缺map FD/错program FD在零source/零listener处拒绝。其他运行场景、环境限制、失败和拒绝阶段见[核心验收](../../../docs/validation/connector-core.md)及机器验证（本地原件 `../../sourceidentity/core-validation.json`，不随Git上传），不以本页概述代替原帧校验。

回归按独立口径记录：相关核心五包race含子测试203 pass；半关闭fake定向测试9个顶层；bootstrap两次受影响回归分别25与26项。最终协议/receiver默认406 pass和7个显式网络skip；最终受保护入口loopback矩阵49 pass、0 skip。不同范围不能相加为无重复总数，更不是正式G01样本。T132/T137未勾选，139/21、G01新样本0/旧0/100不变；名称非原子unverified、路径unknown，完整scope/namespace、exec/改名、长期map回收、生产权限部署与T136管理器退出/接管仍须另验。

## 追加0010/0011：固定内核TFO限制与可执行文件证据

[0010](0010-reject-fast-open-identity.patch)在原accepted socket上先读TCP_INFO，固定6.18.54.1内核拒绝TFO_CHILD，不按ESTABLISHED状态误拒合法半关闭。[独立manifest](../core-tfo-candidate-manifest.json)及其两次真实核心正负回归不改变001–009历史证据。

[0011](0011-executable-call-evidence.patch)在001–010之后增加严格20FD handoff v2及source profile4。可执行文件296B原始快照以原client cookie、PID/TID、启动/采样时间关联，程序名按原路径basename保存、与comm分离；核心校验rawTP真实目标和LSM所指BTF对象内容，逐版本保留v1/v3行为。[新manifest](../core-executable-candidate-manifest.json)记录干净上游apply、完整源树一致和新binary `4fab2b7f44513d57c2629a7b68c3c4ee4634130a10135e59930b0f57925788bb`，此条创建时组合BPF verifier/实际核心转发仍未验证。

配套[组合源码](../../sourceidentity/bpf/executable_online/executable_online.bpf.c)和[运行契约](../../../specs/001-hoopa-proxy-manager/contracts/connector-executable-online-v1.md)要求同调用编号检查先于原connect捕获，采用专用隔离环境；不是生产权限方案。once-only FD清理不承诺raw/LSM完全停止快照，一次拒绝使本世代持续拒绝的可用性限制另列。程序名/路径仍是实验字段，正式identity能力false，PROCESS规则继续拒绝，T132/T137未完成。

## 追加0012：profile4实际线路与ready捕获

[0012](0012-enable-executable-quality-capture.patch)只修正constant启动开关遗漏profile4，并增加真实子进程的环境初始化测试。原0011两流已转发且保留exe记录，但实际hop类型/ID缺失而整轮失败；不放宽判据，保留原run并构建[独立001–012候选](../core-executable-quality-candidate-manifest.json)。新候选在[独立VM](../../../docs/validation/connector-executable-core-positive.md)完成两实际节点、真实ready、原exe绑定和33/33终末全部核对。constant9项含子测试、profile4 outbound race13项含子测试通过。该正向运行不替代新候选的线程/转交/TFO/故障矩阵，不提升正式身份能力。

## 追加0013：独立只读采集诊断

[0013](0013-observe-capture-faults.patch)加入Owner.Diagnostics及受保护GET `/hoopa/capture-health`，既有status schema保持。计数失败为unavailable/null，关闭后不读已释放FD；仅观测，不修改map或自动修复。handoff v2接管与admission前后检查两个raw tracepoint递归遗漏计数，不可读/非零按既有拒绝路径处理。固定LSM MAC不具备同样遗漏计数，零读数不是完整性证明。[独立候选manifest](../core-capture-diagnostics-candidate-manifest.json)记录新源码/二进制与测试，创建时实际VM尚未验证，不借用0012通过结论。

0013随后在[独立实际VM](../../../docs/validation/connector-capture-diagnostics.md)读到scope故障前后0→1、重复读取及前后旧status相同，完整基线33/33。此追加结果不改创建时的manifest，不代表真实raw递归错误注入或完整连续性/恢复验收。

## 追加0018/0019：历史多入口与基础控制门控

[0018](0018-multi-entry-owners.patch)逐字发布已冻结T016 v2 delta，不改变历史5e934bdc核心或其门槛结果。[0019](0019-basic-control-v1.patch)在其上加入独立basic受保护控制v1：closed启动、完整只读快照、认证activate、终末失败与drain历史幂等。公共纯协议源码与核心副本逐字相同。新分支不启用BPF/原始进程身份/计量，不授予这些能力。

从固定T140 reviewed-source新副本实际apply0018/0019，1012文件字节/权限与测试树一致；fresh离线构建SHA `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb` 与测试候选一致。无ldflags，内置1.10.0不表示上游源码版本。详见[manifest](../core-basic-control-candidate-manifest.json)及[有限验收](../../../docs/validation/p1-core-control-candidate.md)。原private/上游完整源码、二进制和日志继续在忽略目录，Git保存文本补丁与摘要。T026整体、真实产品接管与P1尚未完成。
