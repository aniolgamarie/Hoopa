# T136 受保护入口重绑修复的有限实测

2026-10-25。新0016核心在5场隔离VM完成 rollback、replace、identity-conflict、takeover、spawn-window，均VM/规范提取/严格checker为0。共9条应用流，64帧活动来源与失败目标1帧完整零流来源分记，总65帧、167导出。机器记录（本地原件 `../../experiments/mihomo-events/control-rebind-validation.json`，不随Git上传）保存每场制品、流程与原始材料摘要；[构建清单](../../experiments/mihomo-events/core-rebind-candidate-manifest.json)本身保持构建时运行未评估状态，不继承0015通过记录。

## 制品与检查

核心SHA256 `616133dd060b14cdbde68c6d5029f003429d7a8b6adeee4452e8176c63ca80e6`，51,321,239 B；新helper `7364ce47b85d099bc08c31c4012ba22079ed1a9b7ea5f937b8afc274ef0a50ef`，11,380,031 B。源自固定v1.19.26/commit fc8c5a24b16991f98cd736950c17d1aa306a5041，001–016干净应用树与审查树一致。核心保持CGO1；独立guest socket测试为CGO0。原CGO0核心初稿未运行或晋升，保留不覆盖。

改动仅Reserve在Bind之前设置SO_REUSEADDR=1，错误关闭，未启用SO_REUSEPORT。未listen的可复用预留不独占；live listener仍阻止新预留。原先全Bind、再全Listen、再派发handler的顺序不变，不宣称多入口原子可见。身份采集/BPF、source5/manifest2、protected profile及字段能力范围不新增。

Control race为56顶层、含子项200通过，个别测试0fail/skip；两个包vet为0，cmd无测试仅编译。准备72项与7项分开，不合并作运行数。rollback VM实际执行两项socket回归：重复预留允许、首Listen胜出/后续Listen和live Reserve拒绝；server先关闭且旧client读EOF仍开时，立即Reserve+Listen并完成新连接往返。两项PASS、0skip，只有这一真实执行才计测试通过。

## 五场实际结果

| 场景 | 实际行为 | 应用字节与来源 |
| --- | --- | --- |
| `vm-rollback-g2kmeg3g` | 预置20093监听冲突场景下目标启动被拒绝；恢复原完整bundle/rev1，fresh核心PID119/start740，最后显式Stop到rev2 | 原11/11 B、新17/17 B均node:a；15活动帧+失败目标1完整帧，49导出 |
| `vm-replace-vdihp61_` | 完整替换新实例、新generation/epoch，rev1→2，最后Stop到rev3 | 原11/11 B node:a、新17/17 B node:b；15帧、37导出 |
| `vm-identity-conflict-quolgk55` | claim ticks故障使status/stop均503；原流继续，精确恢复原claim后Recover提交停止 | 同一node:a流11+13=24/24 B；7帧、29导出 |
| `vm-takeover-tf1wzz52` | 原管理器SIGKILL/-9，接管核心PID88/start412，原generation/epoch保持 | 两流41/41 B与7/7 B；14帧、26导出 |
| `vm-spawn-window-nh6okw5u` | 原管理器成功spawn、保存实例前退出86；Recover接管原核心PID90/start560 | 两流41/41 B与7/7 B；13帧、26导出 |

五场中原核心、新核心或恢复实例全部使用同一0016制品。这不证明从0015非复用accepted socket立即跨版本重绑：诊断中仅让新socket设置reuse1仍得到errno98；不能把本批视为旧连接迁移或混合版本升级验证。

每条原应用PID/start/cookie与source5 opened、296B已消费executable记录、实际线路/ready和整数终末一一绑定。rollback和replace的原连接实际中断，后续是加入新leaf的新应用实例，没有迁移旧连接。失败目标只保留自己的完整零流来源，不能并入活动流。两接管场景原核心保持；核心退出码/实际reap与仅观察原PID消失分开记录。

## 保留材料与结论

[旧回滚失败](control-replace.md)及[独立绑定诊断](control-bind-diagnostic.md)保持原样。旧口FIN_WAIT2与98/98、同内核reuse对照和本批相同bundle实际恢复成功，共同支持本次socket复用修复针对的旧连接重绑问题；不泛化为任意内核、并发替换保证或连接迁移能力。

CGO0初稿、沙箱下IPC权限失败及helper构建缺artifact父目录的准备失败均保留，不计作本批runtime失败，也不把它们改判通过。独立来源与导出在整理时只读重算，未重跑VM、checker或测试。稳定source/harness、rollback及完整五场运行审查均已完成，无实质矛盾；各报告分别绑定在机器记录中。

主代理按独立acceptance-decision接受本范围T136有限原型，当前139项完成24项；任务勾选由主代理另行落盘。正式G01新增0、旧0/100及P0/P1未放行保持；真实SSH/注销/休眠、其他内核/平台、生产部署和完整性能未验。早期crash、损坏、namespace、admission及认证矩阵仍绑定各自旧hash；未改动相关实现是影响范围依据，不冒充新hash已重跑这些矩阵。控制接口边界仍见[控制原型](control-probe.md)与[契约](../../specs/001-hoopa-proxy-manager/contracts/control-probe-v1.md)。
