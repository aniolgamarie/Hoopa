# 第三方来源与许可

Hoopa 原创管理器代码使用根目录的 [MIT License](LICENSE)。此许可不覆盖下表中上游保留各自许可的材料。这里记录当前源码快照的来源和许可文本；不表示已经发行外部核心或完成未来二进制发行的全部审查。

## 仓库内的上游材料

| 材料 | 来源与版本 | 许可与范围 |
| --- | --- | --- |
| `.agents/skills/speckit-*`、`.specify` 中的模板、脚本和工作流 | Spec Kit / Specify CLI 1.0.6；安装版本见 [integration.json](.specify/integration.json)；项目规格、宪章与后续修改另见相应文件 | [MIT 原文](LICENSES/spec-kit-LICENSE.txt)，Copyright GitHub, Inc.；保留工具来源，不把安装生成记录当作验证结果 |
| `experiments/mihomo-events/patches/` 中修改上游的补丁与上下文 | Mihomo v1.19.26，固定 commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`；修改与顺序见[补丁说明](experiments/mihomo-events/patches/README.md) | 上游 [GNU GPL v3 原文](LICENSES/mihomo-LICENSE.txt)及版权适用；补丁内原创 Hoopa trace 代码的 MIT 来源声明继续保留，修改后的组合核心不能当作 Hoopa MIT 管理器发行 |

以上许可证从本机对应版本的已安装包/固定核心源码中逐字复制，未改写其版权或条款。仓库不包含 Mihomo/GOST 可执行文件、完整上游检出、VM 镜像或下载缓存。未来分发修改核心时，仍需按具体制品准备对应完整源码、修改说明及适用的第三方材料；当前文本补丁快照不能代替这项工作。

## 当前 Go 模块依赖

精确版本与校验和分别见 [go.mod](go.mod) 和 [go.sum](go.sum)。以下文本从各版本的本机模块缓存逐字复制；当前没有 vendor 上游库源码。

| 模块 | 版本 | 保留文本 |
| --- | --- | --- |
| `github.com/spf13/cobra` | v1.10.2 | [Apache 2.0](LICENSES/cobra-LICENSE.txt) |
| `go.yaml.in/yaml/v3` | v3.0.4 | [混合 MIT / Apache 2.0，按原文文件范围](LICENSES/yaml-LICENSE.txt)、[NOTICE](LICENSES/yaml-NOTICE.txt) |
| `github.com/spf13/pflag` | v1.0.9 | [BSD 3-Clause](LICENSES/pflag-LICENSE.txt) |
| `github.com/inconshreveable/mousetrap` | v1.1.0 | [Apache 2.0](LICENSES/mousetrap-LICENSE.txt) |

外部核心本身的传递依赖不属于上表的 Hoopa Go 模块依赖集合；发行具体核心或增加新依赖时，需要针对该制品另行更新清单。
