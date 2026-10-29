# T132 捕获原型准备记录

日期：2026-10-23。用户确认“暂时没有，先完成原型准备”。本轮交付 connect 捕获源码及离线准备工具，**没有运行内核程序，T132 未完成**。任务数仍为 139/21；原 G01 0/100 和 T133–T135 的旧候选、补丁及运行证据不变。

## 已完成的准备

[捕获源码](../../experiments/sourceidentity/bpf/connect_snapshot.bpf.c)只有 `cgroup/connect4` 观察程序和三个有限 map，不是完整采集服务。它按客户端 cookie 保存原始 connect 观察，将进程 leader TGID/start 与调用线程 TID 分开；限制初始 PID namespace、精确 cgroup/netns 和固定 IPv4 loopback TCP 目标。两个 comm 字段始终是未验证观察，未提供路径能力。每条路径均允许原 connect；重复 cookie、范围错配及捕获错误计入本轮故障，不能以保留的部分记录宣称成功。

[离线准备器](../../experiments/sourceidentity/prepare.py)固定执行版本查询、已有 BTF 导出和 clang 编译。C/BTF 先复制为私有快照，命令只读取快照；工具前后摘要变化则失败。每次新建目录，保留成功或失败 manifest；日志/输入有界、步骤有超时。ELF 检查只验证结构与必需 section，不模拟或调用 verifier。精确字段和未来 loader 约束见[准备契约](../../specs/001-hoopa-proxy-manager/contracts/connector-capture-spike-v1.md)。

## 实际验证

| 检查 | 观察结果与限制 |
| --- | --- |
| 准备器单元测试 | 12 项通过，包括缺输入/工具失败、平台限制、ELF 结构错误、超时/输出上限、源文件变化后继续消费固定快照、工具自身变化拒绝及私有权限。使用假工具；不计为身份/内核测试 |
| Python 语法 | py_compile 通过，缓存不进入 Git |
| 第一次真实编译 | `/tmp/hoopa-sourceidentity-9h_0n14p/manifest.json` 记录成功；此版本准备器尚未复制输入，保留为中间证据，不代替最终快照准备结果 |
| 最终真实离线准备 | `/tmp/hoopa-sourceidentity-c4epyjcl/manifest.json` 的四步全部 exit 0；工具 clang 20.1.0、bpftool v5.10.134-17.2.al8.x86_64；目标 BTF 来自本机 5.10.134-17.2.al8.x86_64 |
| 最终对象 | ELF64 little-endian、EM_BPF/ET_REL，含非空 cgroup/connect4、.maps、.BTF、.BTF.ext、license；642640 字节，SHA256 `cdd2320131856014032c944058d5d571f29dbcf428ca2e57bb467e1b3f6118ce`。源码 ABI 静态断言 config=32B、record=112B 通过；不等于 verifier 接受或运行一致性成立 |
| 静态独立复核 | leader 取值路径、初始 PID namespace 限制、UAPI 字节序、仅观察的返回值及 comm 未验证语义核对；loader 故障 map/覆盖连续性、观测 namespace 与精确 cgroup 边界已补入契约，不将缺失的运行保证写成已实现 |

机器归档（本地原件 `../../experiments/sourceidentity/prepare-validation.json`，不随Git上传）绑定稳定源码、工具、BTF、对象、manifest 与测试日志。对象和生成头只保存在私有目录，不提交 Git，也没有生成新的 Mihomo 补丁或替换用户核心。旧 Go proc holder 探针保持原样，本轮没有重跑其历史 100 流或正式身份矩阵。

## 环境事实与故障

scout 的只读盘点确认 clang/llc 注册 BPF targets、BTF 可读、bpftool 存在；有匹配版本的 headers/devel 文件，但没有完整的 `net/core/filter.c` 或 `kernel/bpf/helpers.c`。指定范围内没有找到可用 VM/容器运行材料或 `/dev/kvm`，不能推断宿主其他位置一定不存在。默认沙箱权限仍不足；先前已记录的实际回环上下文也没有获得受控 loader/cgroup 前提。本轮未做 BPF feature probe、load/attach、sudo、mount、sysctl 或网络配置修改。

固定上游 v5.10 的 helper 分派对 task/kernel 读取同时检查 BPF 和 perfmon 能力；编译器存在不等于权限成立。[上游依据](https://github.com/torvalds/linux/blob/v5.10/kernel/bpf/helpers.c#L662-L701)。当前厂商内核的加载/verifier 行为仍需真实验证，不由上游阅读代验。

最终编译之后，默认工具沙箱因 `/tmp` 分区无可用空间而启动失败；首次迁移命令也在 shell 创建 here-document 时失败，Python 未执行。随后改用不创建 shell 临时文件的脚本，将**本轮**两个构建目录逐文件核对 SHA256/权限后移到仓库已忽略的 `private/t132-preparation/`，原 `/tmp` 路径保留符号链接。所有旧任务产物未改。迁移记录单独归档；这次宿主空间故障不是 Hoopa 的满盘注入验收，也不是内核原型失败。

## 保留的四类状态

| 状态 | 本轮判断 |
| --- | --- |
| 已完成 | 捕获观察源码、ABI/范围契约、离线准备器、12 项假工具测试、真实 BTF 编译与输入/对象摘要归档 |
| 进行中 | T132；spike A 的 connect→active 传递、spike B 的 passive→原 accepted cookie 关联、loader/读取端、核心路由接线与真实正反例仍未交付 |
| 失败待决策 | 历史正式 G01 的 0/100 不变；本轮没有新运行身份分母。实际磁盘故障已通过迁移本轮产物解除收尾阻碍，失败事实保留 |
| 环境不足未验证 | verifier/attach、connect 捕获正确性、leader/name 一致性、cookie 生命周期与原入口关联、真实故障计数/覆盖连续性、内核资源成本均未验证 |

本轮 executor 使用 GPT-6.1-sol / medium 实现准备器与测试；scout 使用 GPT-6-luna / high 只读盘点和复核。主代理负责捕获字段与故障语义、权限边界、真实编译、环境异常处理及最终验收。

后续有隔离环境后，先实现有限 loader 并核验 map/config freeze、作用范围、加载权限与清理，再按契约预登记捕获正反例。只有捕获及原入口唯一关联获得证据，才进入完整身份接线与 T137；不能把本次 prepared 状态升级为运行支持。
