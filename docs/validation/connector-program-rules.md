# Source5 原连接程序规则：实现与校准记录

2026-10-25，T140有限原型验收完成。显式配置消费器、0017干净构建和14类真实VM校准均已完成；机器验证（本地原件 `../../experiments/mihomo-events/program-rules-validation.json`，不随Git上传）与[SHA命名候选](../../experiments/mihomo-events/candidates/5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967.json)记录28条流、166帧、462导出及14个完整来源世代。T127正式G01新增样本仍为0，原0/100及0015/0016历史记录保留，P0/P1未放行。

按[有限契约](../../specs/001-hoopa-proxy-manager/contracts/connector-program-rules-v1.md)，新配置入口只在显式 `connect_initiator_exact_program_v1` 下接受原连接程序NAME/PATH及单层AND。匹配区分大小写，字符 `* ? [ ]` 按字面解释；所用字段必须声明于required_fields。来源profile在初始化once内绑定，每个flow保存程序名/路径的值快照。旧process字段继续为unknown，旧配置入口及固定0016控制helper继续拒绝新规则。

新认证只读 `/hoopa/program-rules` 返回独立七字段声明；旧status八个基本字段、存在Owner时追加的admission字段及legacy身份false保持。关键unknown在拨号前拒绝，AND中的已知false支配unknown。程序路径文本不可解码不影响单独有效的程序名文本。

## 已完成

- 纯配置race 15顶层/143含子测试通过，vet通过；核心配置副本除包名与导入路径外逐字对应，独立源码复核未发现重大问题。
- 核心source/规则定向race 28顶层/62含子通过；只读接口及启动拒绝race 4顶层/43含子通过，均无fail/skip；相关vet通过。
- 初始化测试使用10个独立测试子进程，不复制或清零sync.Once。有效修前运行发现两项重复线路请求被接受；修复后同一断言race 1顶层/11含子通过，0fail/skip。首次错误cwd导致的setup失败也单独保存。
- 追加[0017补丁](../../experiments/mihomo-events/patches/0017-source5-program-rules.patch)，从固定上游逐个干净应用001–017，完整树1199项/1002文件与审查目标一致。CGO1核心51,339,954字节，SHA256 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`；详见[构建manifest](../../experiments/mihomo-events/core-program-rules-candidate-manifest.json)。制品保存在private目录，Git中记录源码、补丁与摘要。

## 实际规则校准

独立guest直接启动新核心；14类校准各使用新VM/core epoch、HTTP与SOCKS两个原始连接。覆盖NAME/PATH命中、不命中、大小写、字面星号、两种实际非UTF-8执行路径、AND两个顺序的true/false/unknown。原始执行路径由复制后的真实解释器产生，不能用脚本文件名替代。

全部14场的VM、规范提取、严格checker均返回0。20条转发流在预期实际节点完成每方向33字节，node:a的ready为11–12ms、node:b为31–32ms。8条拒绝流均有成功的原TCP捕获/admission和source5 opened原证据，然后路由前失败、终末0/0，目标夹具没有accept。HTTP/SOCKS入口握手先成功，应用随后得到EOF；没有用timeout或admission失败代替规则unknown。

真实非法UTF8目录不影响有效basename的NAME命中；非法basename使NAME文本unknown。AND两种顺序中，已知false使规则不匹配并走后续node:b，已知true与unknown组合拒绝。拒绝事件的payload为共用`unknown_or_route_failure`，场景归因来自配置、原始文本及对照，不单靠该字段证明唯一内部失败分支；目标侧零accept也不证明没有SYN或短暂backlog。

本轮使用可信root实验客户端和固定隔离Linux；不重复声称新的降权/多平台验收，不承诺完整部署或生产launcher。正式100个短流槽位须在此能力校准完成后由T127预登记。

## 失败待决策与环境未验证

当前无未决生产代码失败；修前重复线路请求失败已修复并保留原记录。本批14场实际VM均通过，聚合器的Python3.8兼容及工具路径准备错误保留。其他内核、macOS/Windows及真实SSH/休眠行为没有新证据。进行中：T127新版oracle与正式100槽位登记，完整T127–T129矩阵尚未完成。

本阶段使用executor（观察到gpt-6.1-sol/medium）实现有界配置/测试与VM夹具，scout（观察到gpt-6-luna/high）独立取证和源码复核；主代理负责once语义、集成、干净构建及最终校准裁决。单测与审查不能替代真实流验收。
