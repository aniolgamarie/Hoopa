# Hoopa 验证指南

仓库已进入 DB-free P1 实施，Linux/amd64 的[基础服务与 CLI](../../docs/validation/p1-service-lifecycle.md)已有限验收；构建与 control-only 示例见 [README](../../README.md)。通用配置、订阅与完整产品故障矩阵仍未完成。
固定版本两核的受控收发、认证与真实 Codex 接入已运行；完整 G01–G07 门槛没有因此通过，实际状态见[任务记录](../../docs/task-log.md)。
命令契约见 [contracts/cli.md](contracts/cli.md)，运行断言见[原型验收](../../docs/prototype.md)。

实测后完整计量按[ADR 005](../../docs/adr/005-telemetry-replan.md)重规划；接收journal与原入口身份探针的当前运行/权限状态见[追加验收](../../docs/validation/telemetry-replan.md)。stock完整事件能力未实现，不自动替换核心。

当前状态（2026-10-27）：140项/47完成，T033 已接线并通过实际核心连续性及一次重启场景；T034 产品适配器/完整矩阵仍未完成，采集器明确 unavailable。详见 [tasks.md](tasks.md) 和[服务契约](contracts/service-lifecycle-v1.md)。

历史状态（2026-10-26，当时记录）：140项/27完成，[固定0017](../../experiments/mihomo-events/core-program-rules-candidate-manifest.json)已完成[T127受限正式身份验收及T012评审](../../docs/validation/source5-formal-identity.md)。[T128恢复准备](../../docs/validation/source5-recovery-preparation.md)取得三个真实传输case的新6/6，核心/collector崩溃重开等完整矩阵与T129尚未完成。旧0/100、首轮60/100和准备失败均保留；P0/P1未放行，尚无产品CLI或正式平台支持。

以下按历史候选记录已有实验命令，运行旧命令不会验证0017。当前source5验证依赖各自预登记的私有隔离VM材料；不要把旧命令的candidate路径或SHA直接替换为新制品以继承通过。

## 2026-10-23：早期候选与局部验收记录

同源事件的固定源码、计数帧解码、隔离增强候选见[T126记录](../../experiments/mihomo-events/README.md)。当时T127的100流身份断言失败，T128/T129未执行，P1未放行，见[历史结果](../../docs/validation/native-candidate.md)。

2026-10-23实施已完成T130同拓扑诊断及T131具体裁决，见[实际前提](../../docs/validation/p0-prerequisites.md)与[ADR006](../../docs/adr/006-source-identity-proof.md)；T132仍缺正向身份前提。T133初期checkpoint/严格接收/只读pending及真实生命周期有[部分结果](../../docs/validation/source-lifecycle.md)，其本地闭环完成情况见下段新验收；T134与T135已完成下述局部验收。只有失败相关环境、方案或实现输入发生可观察变化且有明确因果预期时才重试，文档修改或仅重编相同源码不构成理由，旧0/100及正式分母保留。T137冻结完整候选并确认身份smoke成功后，才启动新的正式100流。T136先提供控制/变更harness；T138负责P2生产传输/回收及新制品冻结/必要复验，T072/T077核对来源与能力记录；T139在首次旅程前提供CLI。当前139项完成21项，P0/P1未放行；[修订记录](../../docs/validation/plan-task-review.md)不代替运行验收。

这三项后续实施依据[ADR007](../../docs/adr/007-p0-evidence-closure.md)及T132–T134修订：用户已选择connect_initiator_v1，建连快照及FD共享/转交限制见规格；身份先验证connect事实与原入口唯一关联，后端仍未实证且默认关闭。来源恢复改为原帧重放+派生pending/gap报告，撤回未实现的强制v2恢复引用图；质量字段profile先冻结，协议ready/真实两线路随后独立验收；固定候选SOCKS5回复头缺少成功码校验的定向修复另以003补丁/测试记录，该次003记录不代表新完整候选或两线路验收，后续006/007结果分别列于下文。不要在用户核心或旧001/002副本原位应用。

随后004/005已接入共用v1严格校验、来源重开/派生恢复及确认前缀发送，修复HTTP回复预读边界并从干净上游实际应用001–005构建新局部候选，见[新manifest](../../experiments/mihomo-events/closure-candidate-manifest.json)及[实际结果](../../docs/validation/source-recovery-closure.md)。原组合首次active失败保持记录；新增独立健康入口先验证转发，再开始257字节计量场景。只读`sourceinspect --frames PATH --manifest PATH`可核对冻结配对文件，输出含flow明细，应只保存在私有目录；它不持writer锁、Sync或封存旧epoch，缺final仍为pending。再经collector离线/ACK停滞及receiver/core重开真实测试后，[T133本地最小闭环已完成](../../docs/validation/source-transport.md)；T132/T137和正式门槛仍未放行；T134随后完成的范围见下段。

新的传输子实验默认skip，必须明确提供固定制品和回环授权：

```sh
HOOPA_CLOSURE_NETWORK_TEST=1 \
HOOPA_CLOSURE_CANDIDATE=/tmp/hoopa-t133-closure-artifact-l35s9_fn/mihomo-closure \
HOOPA_CLOSURE_CANDIDATE_SHA256=bfc1df3a99871f2b1257e4b28b58b64a97b6289840b6cd63a50c6b86ec75c6d2 \
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-go-cache \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -count=1 \
  ./experiments/mihomo-events -run '^TestHoopaClosureTransport'
```

它只启动自有私有回环实例，不运行旧G01身份100流；collector离线阶段独占bind但不listen，源终末已持久后才交接给实际接收器。机器证据（本地原件 `../../experiments/mihomo-events/transport-validation.json`，不随Git上传）绑定测试文本、原核心SHA及结果；截至T133完成时为139项/19完成；该局部结果不代表正式G02/P0或产品支持，最新任务计数见开头。


## T134局部候选与显式复验

006在全新固定上游副本依次应用001–006后构建；[候选manifest](../../experiments/mihomo-events/quality-candidate-manifest.json)绑定补丁、源码树、工具链和二进制摘要，[质量验收](../../docs/validation/quality-ready.md)保留失败、成功和未验证范围。需要本地回环执行权限以及仍存在的私有制品，不下载依赖或操作用户核心。从仓库根执行：

```sh
HOOPA_QUALITY_NETWORK_TEST=1 \
HOOPA_QUALITY_CANDIDATE=/tmp/hoopa-t134-artifact-ul72y63w/mihomo-quality \
HOOPA_QUALITY_CANDIDATE_SHA256=9d52edee280d7e4893911e3c4a6ae43150cf4914421431f7ee19567505459b29 \
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -count=1 -timeout=60s \
  ./experiments/mihomo-events -run '^TestHoopaQuality'
```

固定最终run为5条成功流加3条失败后成功流，共8流/11attempts，每流每方向257B；真实核心是普通构建，Go验收程序及独立核心包测试开启race，不能称整个子进程使用race。inline节点provider为空/null，未验证命名订阅provider；T132身份和正式G01–G03不因该局部结果通过。核心的v2显式参数为`HOOPA_TRACE_PROFILE=2`及绝对路径`HOOPA_TRACE_ROUTE_MAP`，仅实验harness生成并交付，不提供产品CLI承诺。
## T135受保护实验入口与显式复验

007在同一固定上游的全新私有副本依次应用001–007，check/apply及整树内容/路径/权限一致后离线构建。最终版本`hoopa-t135-guard-final`，SHA256 `7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f`；[manifest](../../experiments/mihomo-events/capability-candidate-manifest.json)、机器验证（本地原件 `../../experiments/mihomo-events/capability-validation.json`，不随Git上传）和[分层验收](../../docs/validation/protected-entry.md)绑定固定输入与旧失败。制品只保存在私有/tmp，不覆盖旧候选或用户核心。

从仓库根执行以下显式回环验收；需要授权的本地socket权限和仍存在的固定制品，不下载依赖。默认gate为skip，显式启用但缺候选或SHA不匹配必须失败：

```sh
HOOPA_CAPABILITY_NETWORK_TEST=1 \
HOOPA_CAPABILITY_CANDIDATE=/tmp/hoopa-t135-artifact-tg95ee9o/mihomo-guard \
HOOPA_CAPABILITY_CANDIDATE_SHA256=7c022d3263cf5c16d72fd68834041ae2bfb15e748283b6aa75d529c1fdf4440f \
GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/hoopa-mihomo-events-buildcache \
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -count=1 -timeout=90s \
  ./experiments/mihomo-events -run '^TestCapability'
```

最终真实日志`/tmp/hoopa-t135-artifact-tg95ee9o/capability-network-final.jsonl`为2顶层、含子测试49项pass，0fail/skip，包耗时2.839s；harness开启race，core是普通构建，另有核心包race，不能称整个核心子进程有race插桩。

[protected-core-v1](contracts/protected-core-v1.md)只接受有界JSON：显式`HOOPA_GUARD_CONFIG_SHA256`与`HOOPA_GUARD_GENERATION`，`-d`已有0700私有目录和`-f`已有0600绝对JSON文件。校验、hash及核心Parse消费同一份原字节；所有入口/controller资源先bind后listen，失败清理。每个命名mixed入口有独立认证，保留原序首匹配；控制面仅认证`GET /hoopa/status`和`POST /hoopa/drain`，其他API授权后409。trace另要求同generation、完整类型一致mapping及可写来源。

generation/hash在运行中不可变，SIGHUP忽略；drain关闭新accept并保留已接受TCP，完整replace另起独立新进程/入口，旧流不迁移。所有进程实例/名称/路径规则及不支持组合在加载时拒绝，identity三字段仍false，不能以拒绝冒充正向身份。仅Linux/amd64 IPv4 loopback实验；同UID无OS隔离。T132/T127/T128/T129/T136/T137/T018保持未完成，P0/P1及正式门槛未放行。

## 早期离线准备与只读仓库检查

以下两段保留最初尚未加载BPF时的准备状态；后续隔离VM身份验收见本页开头。离线命令仍只代表其各自有限作用，不负责重跑当前身份或恢复矩阵。

T132 的[离线准备说明](../../experiments/sourceidentity/README.md#t132-捕获子步骤与离线准备)提供固定工具/BTF输入的编译命令：默认 `--profile capture`，另可用 `--profile lifecycle` 编译同对象的connect4与sockops观察。编译器不加载对象或提供关联查询；该命令会创建私有构建目录，不属于下面的只读仓库检查。另已实现[有限loader准备](../../docs/validation/connector-loader-session.md)，仅fake接口验证，不执行默认加载；真实运行仍须隔离环境与另行加载授权。当前未运行verifier、load/attach或身份关联，关闭link也不证明稳定快照。详见[捕获准备验收](../../docs/validation/connector-preparation.md)和[生命周期准备验收](../../docs/validation/connector-lifecycle-preparation.md)，T132及正式门槛保持未完成。

新增`--profile gated`离线构建与对象检查，固定三份源文件、3程序/6map，见[停止准备验收](../../docs/validation/connector-stop-preparation.md)。runner同名profile未来将额外执行BPF_PROG_TEST_RUN关闭gate，这仍是内核执行，不能在当前环境直接运行。当前仅完成模型/fake测试与离线构建/解析；writer_stop候选记录不签发身份或完整性能力。命令默认profile和显式加载开关不变。

在仓库根目录执行，仅读取已有文件；不创建应用目录或账本：

```sh
python3 - <<'PY'
import json
from pathlib import Path
root = Path('specs/001-hoopa-proxy-manager/contracts')
for path in sorted(root.glob('*.schema.json')):
    schema = json.loads(path.read_text())
    assert schema['$schema'] == 'https://json-schema.org/draft/2020-12/schema'
    print('JSON parsed:', path)
try:
    from jsonschema import Draft202012Validator
except ImportError:
    print('jsonschema unavailable: schema meta-validation UNVERIFIED; do not install for this check')
else:
    for path in sorted(root.glob('*.schema.json')):
        Draft202012Validator.check_schema(json.loads(path.read_text()))
        print('Schema valid:', path)
PY
```

这验证文档结构，不证明运行接口、JCS、事务、路由或计量正确。相对引用验证需要本目录全部文档写入完成。

## P0：先准备证据，再裁决

以下为完整执行清单，已执行部分与未关闭门槛分别记录。工具链候选及模块版本见 [research.md](research.md)，执行前记录版本、来源与摘要；
按[实验说明](../../experiments/gost-mihomo/README.md)构建和显式运行，不以单元测试代替真实核心门槛。

1. 准备明确路径的固定 Mihomo/GOST、测试订阅、两个来源 fixtures、受控 HTTP/TCP 端点与真实 Agent CLI。
   版本输出参数按各固定版本帮助核对；不假定两者均支持 `--version`。用 `sha256sum` 记录二进制摘要。
2. 使用用户私有临时目录与空闲 loopback 端口，按[原型最短路径](../../docs/prototype.md#2-最短端到端路径)手工启动实验实例。
   GOST 的 services/chains/metrics 模板以该文档为准，controller 凭据与真实认证留在私有目录。
   可运行 harness 的 Mihomo 1.19.26 / GOST 3.3.1 模板已通过实际校准，其他版本未验证。
3. 先用受控端点比较直接 Mihomo 与 GOST→Mihomo 的路径，再做真实 Agent 的正常及持续连接工作流。
   校准示意命令（仅在变量均已设置、对应隔离实例就绪后）：

   ```sh
   curl --proxy "$EXPERIMENT_PROXY_URL" --noproxy '' "$CONTROLLED_ENDPOINT_URL"
   ```

   `--noproxy ''` 避免父环境绕过代理；该对照不能代替真实 Agent、SOCKS5 或信号/后代验收。
4. 每次保存 manifest、原始脱敏 metrics、events、连接证据、ledger-check 与 result；文件清单见 prototype。
   真值集在测试前登记，包括两个同名进程、子进程、PID 复用与短于采样周期的连接；不能事后删除失败样本。
5. 按故障矩阵 F01–F33 执行相关最小探针；故障操作仅限已核验启动身份的隔离实例。
   计量必须覆盖 HTTP CONNECT/SOCKS5、长短连接、失败、重放、归零、series 消失和大数精度。
   路由必须覆盖无 GOST 的应用上下文、规则重叠顺序、路由前身份、未知拒绝、失效 pin 与完整快照回滚。
   SQLite 两候选执行相同故障集；只读检查比较文件 hash/mtime/树及进程集合，atime 和无关后台写入单列。

| 七项 P0 门槛 | 本轮状态 | 通过所需证据 |
|---|---|---|
| G01 原始身份与短连接 | **正式身份断言failed，0/100**；门槛未关闭 | 同拓扑前提、真实身份、100预登记样本及完整负向矩阵 |
| G02 计数与 recorder | 100流终末/503重投等子断言通过；完整矩阵未通过 | 计数边界、未投递/重放、世代、精度及对账 |
| G03 计时与实际链 | 已观察单DIRECT路线事件；完整矩阵未通过 | 建立/首字节边界、结果分类及两节点原连接关联 |
| G04 独立运行/接管 | 仅原harness存活子观察；完整验收未执行 | 管理器崩溃、SSH/注销/休眠、身份冲突 |
| G05 规则与变更 | 纯规则测试及共享入口子证据；完整验收未执行 | 配置级能力拒绝、严格顺序、逐持久边界恢复、过期候选拒绝 |
| G06 驱动与只读 | 未执行 | 双驱动同集比较、WAL/SHM、故障与构建 |
| G07 规模与安全 | 原组合9项认证通过；完整保护/规模未验收 | 各端独立授权、0/1/5入口、规模、清理与资源测量 |

当前结果依据[隔离候选](../../docs/validation/native-candidate.md)及[门槛裁决](../../docs/adr/002-p0-core-gates.md)；表中未完成项不推断主机上一定缺少二进制或网络材料。
详细裁决及失败出口见 [research.md](research.md#12-p0-裁决表与研究完成状态)。失败升级主代理决策，不能以完整 UI 或全 unknown 绕过。

## 未来 P1：实现存在后的用户路径

以下仅定义验收脚本应执行的行为；命令尚不可运行。先完成 Go module、实现、固定依赖与 P0 必要裁决，
再运行届时仓库规定的格式、vet、单元/适配/集成及适用 race 检查。

```sh
hoopa init --mihomo "$MIHOMO_PATH"
hoopa status
hoopa doctor
hoopa start --control-only
hoopa config plan --input "$LOCAL_INTENT_PATH" --output "$PLAN_PATH"
```

此时 init 不联网/不建账本，status/doctor 不启动或修复；control-only 只启动管理服务。
本地 intent 必须引用已有不可变来源；需要下载时先显式 `subscription update ID`，再重新预览。
从 Plan 读取 blockers、输入摘要、base_revision、impact.runtime_action、受影响应用、连接影响与 plan_digest；
存在阻断或未知身份时先处理，不自行改写摘要。读取后提交原文件：

```sh
hoopa config apply --plan "$PLAN_PATH" --confirm "$PLAN_DIGEST" --accept-disruption
hoopa app inspect agent --mode managed
hoopa run --app agent --mode managed -- "$AGENT_PATH"
```

`--accept-disruption` 仅在本次已检查并接受计划影响后使用。P1 完整应用披露 start/replace，并实际核验后提交。
首次提交后的普通 `hoopa start` 使用最后已提交且可用快照；没有快照时失败。
分别测试 managed/inherit/direct：只变子环境，父环境不变；managed 不可用须失败，direct 不承诺绕过 TUN。
不计量路径不得要求安装 GOST 或创建 SQLite；`--meter` 在相应能力交付前明确拒绝。

机器接口最小只读验收：

```sh
printf '%s\n' '{"api_version":"v1","request_id":"smoke-status-1","operation":"status.get","params":{}}' | hoopa api v1 status.get
```

验收还须覆盖：未知/重复字段、重复 key、超限及尾随正文；非终结收据轮询；响应丢失同 ID 重试；
同 ID 改正文拒绝；两个候选同 base revision 时第二个写入前 CONFLICT；输入摘要变化拒绝；
实际应用成功但发布失败、完整回滚失败及 degraded。阶段与不可跨资源原子性见 [data-model.md](data-model.md)。

## T132 有限关联模型检查（不加载内核）

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_association_model.py' -v
python3 -B experiments/sourceidentity/association_model.py
```

14 项测试通过只说明模型满足所列断言；报告中三种 unsafe_counterexamples 应各发现错误候选，其中漏 active 和错原 FD 为零 fault 反例。它们不是通过的身份能力，也不是正式 G01 新样本。旧 observer 输出不能直接输入本模型：passive 的实测 netns 和原 FD witness 还未取得。按[关联契约](contracts/connector-association-model-v1.md) A–E 顺序验证范围/连续性、版本化关联观察、原 FD 来源和在线接线；[验收记录](../../docs/validation/connector-association-model.md)保留环境未验证项。

## T132 原 accepted 见证检查

使用已核验的本地Go 1.27.3，显式GOTOOLCHAIN=local、GOPROXY=off及可写的私有TMPDIR/GOTMPDIR/GOCACHE；不自动下载工具链。最小包级检查：

```sh
GOTOOLCHAIN=local GOPROXY=off go test -race -count=1 -run '^TestFake' ./experiments/sourceidentity/acceptedwitness
GOTOOLCHAIN=local GOPROXY=off go vet ./experiments/sourceidentity/acceptedwitness
```

真实loopback单独设置HOOPA_WITNESS_LOOPBACK=1并选择`-run '^TestLoopback' -timeout=25s`，只使用自建127.0.0.1临时端口。本机实际SO_COOKIE关闭窗口通过，NETNS option 71不支持导致完整正例skip；package exit0也不能掩盖skip。固定工具链路径、精确命令和摘要见[验收](../../docs/validation/accepted-socket-witness.md)。[见证契约](contracts/accepted-socket-witness-v1.md)不提供在线身份，真实BPF/核心接线仍按原门槛执行。

## T132 小规模校准准备

新增namespaced profile、独立serve/client fixture及离线checker。测试与离线构建已完成，真实内核运行未验证；完整命令与输入格式见[校准执行契约](contracts/connector-calibration-v1.md)，证据见[验收](../../docs/validation/connector-calibration.md)。

未来隔离lab必须先固定产物和预登记，等待observer的attached marker，再由外部launcher将驱动放入既有目标cgroup；驱动负责启动server并在listening之后启动两个独立client。新增[完整命令及材料契约](contracts/fixture-run-v1.md)约束14秒余量、同一boot/PID/time namespace、私有fixture副本和11文件输出。观察停止后，使用 `check_fixture_run.py` 同时核对运行目录、observer结果与外部scope声明；检查输出放在运行目录外。58项定向测试通过，见[验收](../../docs/validation/fixture-run.md)，其中真实子进程仅无网络Python程序。checker不执行BPF或cgroup操作，exit0不放行身份能力。当前缺隔离环境，本机NETNS失败不重试，T132及正式门槛仍未勾选。

## 未来 P2/P3：不能由 P1 代验

P2 对相同数据/归属/保留版本验证过滤、稳定排序、分页、Top N 与同名展开，总计保留隐藏和未知；
游标过期返回 SNAPSHOT_EXPIRED。启用/缩短保留策略先 plan/apply 确认，之后自动清理；
只读查询显示 pending，不执行清理，延长策略不恢复已删事实。质量重算同口径样本，禁止平均 p95 或补造缺口曲线。
详见[统计设计](../../docs/traffic-statistics.md)和[数据模型](data-model.md)。
P3 分别记录构建、回环、真实应用、SSH/注销/休眠和性能证据；交叉编译不等于平台正式支持。
迁移先预览备份，显式执行再替换。执行记录分别标为已完成、进行中、失败待决策、环境不足未验证。
