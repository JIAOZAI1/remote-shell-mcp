# remote-shell-mcp

**让 AI Agent 像使用本地工具一样操作远程服务器，同时明确每次操作的目标。**

本地运行的跨平台 MCP Server，为 Codex、Claude Code、Pi 等客户端提供服务器管理、远程命令、文件操作和持久化终端。连接认证由服务端处理，Agent 不需要拼接 SSH 命令或接触私钥。

[下载二进制](https://github.com/JIAOZAI1/remote-shell-mcp/releases/latest) · [配置示例](configs/config.example.json) · [MIT License](LICENSE)

## 目录

- [背景与设计](#背景与设计)
- [功能概览](#功能概览)
- [安装与配置](#安装与配置)
- [接入 Codex](#接入-codex)
- [接入 Claude Code](#接入-claude-code)
- [接入 Pi](#接入-pi)
- [第一次使用](#第一次使用)
- [本地日志](#本地日志)
- [工具参考](#工具参考)
- [常见问题](#常见问题)
- [连接与安全边界](#连接与安全边界)
- [自动构建与发布](#自动构建与发布)
- [测试与当前限制](#测试与当前限制)
- [开源协议](#开源协议)

## 背景与设计

AI 编程助手通常擅长操作本地项目，但部署、排障、读取服务日志和运行长时间任务往往发生在远程服务器。直接让 Agent 调用 SSH，会反复涉及目标地址、密钥、路径转义、会话状态等细节，也容易混淆本地与远程操作。

本项目将这些操作封装为 MCP 工具：用户在本地配置凭证和可信主机密钥，Agent 通过稳定的 `server_id` 选择服务器，再表达“执行命令”“读取文件”“创建终端”等意图。

```text
Codex / Claude Code / Pi
          │ MCP（stdio）
          ▼
本地 remote-shell-mcp
  ├─ 配置与凭证引用
  ├─ 主机密钥校验
  └─ 工具调用日志
          │ SSH / SFTP
          ▼
指定远程服务器（命令、文件、tmux）
```

- **屏蔽传输细节，不隐藏目标**：远程工具必须指定服务器；连接失败不会回退到本地执行。
- **两种执行方式**：`remote_exec` 用于独立命令，远端 tmux 用于保留目录、环境和运行中进程的连续任务。
- **不需要远端部署 MCP Server**：远端提供 SSH/SFTP 即可；持久化终端另需 tmux。
- **不是权限沙箱**：工具使用配置账号的权限。建议从测试机、专用低权限账号和只读操作开始。

## 功能概览

| 能力 | 用途 |
|---|---|
| 服务器添加、搜索与删除 | 本地持久化连接配置，通过 ID 选择目标 |
| 独立命令执行 | 指定目录和超时，返回 stdout、stderr、退出码及截断状态 |
| 文件读写与目录列举 | 分范围读取文本、显式覆盖、控制父目录创建、目录分页 |
| 文件上传与下载 | 在 MCP Server 所在机器与远程服务器之间传输文件 |
| 持久化终端 | 在远端创建、列举、发送输入、读取及关闭 tmux 会话 |
| 本地调用日志 | JSONL 日志按天轮转，目录和保留天数可配置 |
| 跨平台二进制 | Linux、macOS、Windows，均提供 amd64 / arm64 |

共提供 **14 个 MCP 工具**。适用于远程排障、检查部署结果、读取日志、传输构建产物和持续运行开发任务；具体接口见[工具参考](#工具参考)。

## 安装与配置

### 1. 准备运行环境

| 位置 | 要求 |
|---|---|
| 本地（运行 MCP Server 的机器） | Linux、macOS 或 Windows；可连接目标服务器；具有私钥和已核验的 `known_hosts` 文件 |
| 远程目标 | SSH 服务、SFTP 子系统、POSIX shell；配置用户可访问目标工作目录 |
| 持久化终端 | 远端已安装 tmux；缺失时报错，不自动安装 |
| MCP 客户端 | 支持启动本地 stdio 服务；Pi 需安装 MCP 适配扩展 |

这里的“本地”是 **MCP Server 进程所在机器**。如果客户端运行在 WSL、容器或远程开发环境中，二进制、配置和私钥也必须能从该环境访问，且二进制平台应与实际运行环境一致。

### 2. 下载二进制

从 [Releases](https://github.com/JIAOZAI1/remote-shell-mcp/releases/latest) 下载对应文件和 `SHA256SUMS`，核对校验值后再执行：

| 系统 | amd64（Intel / AMD） | arm64（包括 Apple Silicon） |
|---|---|---|
| Linux | `remote-shell-mcp-linux-amd64` | `remote-shell-mcp-linux-arm64` |
| macOS | `remote-shell-mcp-darwin-amd64` | `remote-shell-mcp-darwin-arm64` |
| Windows | `remote-shell-mcp-windows-amd64.exe` | `remote-shell-mcp-windows-arm64.exe` |

Linux/macOS 可将下载文件重命名为 `remote-shell-mcp`，放到固定位置并赋予执行权限：

```sh
chmod +x /absolute/path/remote-shell-mcp
/absolute/path/remote-shell-mcp --help
```

Windows 可重命名为 `remote-shell-mcp.exe`，例如放在 `C:/Tools/remote-shell-mcp.exe`。JSON 路径可使用 `/`，或将反斜杠写成 `\\`。二进制未做代码签名或 macOS 公证。

<details>
<summary>从源码构建</summary>

需要 Go `1.27.1`（版本以 `go.mod` 为准）：

```sh
git clone https://github.com/JIAOZAI1/remote-shell-mcp.git
cd remote-shell-mcp
go build -o remote-shell-mcp ./cmd/remote-shell-mcp
```

Windows 构建时将输出名改为 `remote-shell-mcp.exe`。

</details>

### 3. 准备私钥与可信主机记录

1. 为目标服务器准备专用低权限账号，并将对应公钥配置到远端。
2. 将私钥保留在本地，仅允许必要用户读取。支持加密和未加密私钥，不要将私钥内容放进对话或提交到仓库。
3. 通过管理员、云控制台等可信渠道核对服务器主机密钥指纹，将已验证的主机公钥写入本地 `known_hosts`。非默认端口的记录应匹配 `[host]:port`。

可复用已核验的 `~/.ssh/known_hosts`。`ssh-keyscan` 只能收集公钥，不能证明主机身份，不应未经核验就直接信任。未知或发生变化的主机密钥会被拒绝。

**目前不支持** SSH 密码认证、SSH Agent、跳板机或自动读取 `~/.ssh/config`。

### 4. 创建服务端配置

默认配置路径为用户主目录下的 `.remote-shell-mcp/config.json`，可用 `--config` 覆盖。Linux/macOS 先创建私有目录：

```sh
mkdir -p "$HOME/.remote-shell-mcp"
chmod 700 "$HOME/.remote-shell-mcp"
```

参考 [config.example.json](configs/config.example.json) 创建配置；下面以 Linux 用户 `alice` 为例，**请替换为自己的真实绝对路径**：

```json
{
  "known_hosts": "/home/alice/.ssh/known_hosts",
  "credentials": {
    "dev-key": "/home/alice/.ssh/id_ed25519"
  },
  "logging": {
    "directory": "logs",
    "retention_days": 30
  }
}
```

macOS 用户目录通常为 `/Users/alice`，Windows 可写 `C:/Users/alice/...`。配置文件中的 `~`、`$HOME` 不会自动展开；`known_hosts` 和私钥路径必须为绝对路径，且 `known_hosts` 文件必须已存在。建议限制配置文件权限，Linux/macOS 可使用 `chmod 600`。

| 字段 | 含义 |
|---|---|
| `known_hosts` | 本地可信主机公钥文件，必填 |
| `credentials` | 自定义凭证引用 → 本地私钥路径，例如 `dev-key` |
| `credential_passphrase_env` | 可选，凭证引用 → 私钥口令环境变量名 |
| `logging.directory` | 日志目录，默认配置文件旁的 `logs`，支持绝对路径 |
| `logging.retention_days` | 保留天数，默认 30，包含今天；详见[本地日志](#本地日志) |

服务器地址等记录由 `server_add` 写入同目录的 `servers.json`，首次无需手动创建：

```text
~/.remote-shell-mcp/
├── config.json       # 用户维护：凭证引用、known_hosts、日志配置
├── servers.json      # 工具维护：服务器列表
├── servers.lock      # 运行期间的配置写锁
└── logs/             # 默认调用日志目录
```

> **同一配置目录不能由多个实例同时使用。** Codex、Claude Code、Pi 及不同会话可能各自启动进程。请串行使用，或为各实例准备独立配置目录（仅换文件名不够）；不要通过删除运行中实例的锁文件绕过限制。

### 5. 加密私钥的口令（可选）

在 `config.json` 顶层加入以下字段，只保存变量名，不保存口令：

```json
{
  "credential_passphrase_env": {
    "dev-key": "REMOTE_SHELL_DEV_KEY_PASSPHRASE"
  }
}
```

这是需要合并到前述配置的片段，不是独立完整配置。未加密私钥无需该字段。

使用 Bash 时，可隐藏输入口令，再从同一终端启动客户端：

```bash
read -r -s -p 'Private key passphrase: ' REMOTE_SHELL_DEV_KEY_PASSPHRASE
printf '\n'
export REMOTE_SHELL_DEV_KEY_PASSPHRASE
codex  # 或 claude / pi，每次选择一个
unset REMOTE_SHELL_DEV_KEY_PASSPHRASE
```

Codex 还需配置下文的 `env_vars` 转发。不要用包含真实口令的 `--env KEY=value` 命令或明文配置代替安全注入。已启动的 GUI 不会自动继承新环境变量，需要从正确环境重启；环境变量也不是密钥保险库，应避免环境转储。

## MCP 客户端接入说明

下面三种接入方式任选其一，示例中的二进制和配置路径均需替换。`remote-shell` 是客户端中的 **MCP 服务名称**，与后续返回的远程服务器 `server_id` 不同。

客户端会自行启动并管理本地服务，**无需提前运行一个后台 Server**。本项目仅提供 stdio，不监听 HTTP 端口，不使用 `--url` 接入。终端直接运行程序时会等待 MCP 输入，不是交互式 shell，也不是卡住；接入客户端前请先退出手动启动的实例。

### 接入 Codex

用 CLI 注册：

```sh
codex mcp add remote-shell -- /absolute/path/remote-shell-mcp --config /absolute/path/config.json
codex mcp list
codex mcp get remote-shell
```

也可直接编辑 `~/.codex/config.toml`；若已通过 CLI 添加，则修改已有段落，不要重复定义：

```toml
[mcp_servers.remote-shell]
command = "/absolute/path/remote-shell-mcp"
args = ["--config", "/absolute/path/config.json"]
startup_timeout_sec = 20
tool_timeout_sec = 660
# 使用加密私钥时，显式转发已存在于 Codex 进程环境中的变量：
env_vars = ["REMOTE_SHELL_DEV_KEY_PASSPHRASE"]
```

未使用加密私钥时可删除 `env_vars`。客户端工具超时设为 660 秒，为服务端最长 600 秒的操作预留连接开销；它不会改变服务端操作上限。

重新启动 Codex，在会话中用 `/mcp` 检查工具，然后要求 Agent 调用 `server_list`。详见 [Codex MCP 文档](https://developers.openai.com/codex/mcp)。

### 接入 Claude Code

为当前用户注册（跨项目可用）：

```sh
claude mcp add --transport stdio --scope user remote-shell -- /absolute/path/remote-shell-mcp --config /absolute/path/config.json
claude mcp list
claude mcp get remote-shell
```

若只在当前项目使用，将 `--scope user` 改为 `--scope project`，配置写入项目 `.mcp.json`；也可手动创建或合并：

```json
{
  "mcpServers": {
    "remote-shell": {
      "type": "stdio",
      "command": "/absolute/path/remote-shell-mcp",
      "args": ["--config", "/absolute/path/config.json"]
    }
  }
}
```

项目配置可能需要用户批准。不要把个人私钥路径、口令或生产服务器配置当作公共项目配置提交。

重新启动 Claude Code，在会话中执行 `/mcp` 查看连接，并让 Agent 调用 `server_list`。加密私钥的环境变量须在启动 `claude` 前注入；长操作如被客户端提前取消，可按客户端版本调整 `MCP_TOOL_TIMEOUT`（毫秒），例如 Bash 中使用 `MCP_TOOL_TIMEOUT=660000 claude`。详见 [Claude Code MCP 文档](https://code.claude.com/docs/en/mcp)。

### 接入 Pi

**Pi 核心不内置 MCP 客户端。** 以下使用第三方扩展 [`pi-mcp-adapter`](https://www.npmjs.com/package/pi-mcp-adapter)，不是把 JSON 放进 Pi 就自动生效。扩展拥有本机访问权限，安装前请审查并信任其来源。

1. 安装扩展后重启 Pi：

   ```sh
   pi install npm:pi-mcp-adapter
   ```

2. 创建或合并全局 `~/.config/mcp/mcp.json`（所有项目），或当前项目的 `.mcp.json`：

   ```json
   {
     "mcpServers": {
       "remote-shell": {
         "command": "/absolute/path/remote-shell-mcp",
         "args": ["--config", "/absolute/path/config.json"],
         "requestTimeoutMs": 660000
       }
     }
   }
   ```

3. 重启 Pi，运行 `/mcp` 查看服务，运行 `/mcp reconnect remote-shell` 连接并刷新工具列表。扩展默认延迟连接，未连接状态不一定表示配置失败。也可通过 `/mcp setup` 配置或导入其他客户端设置；不要假设 Codex 配置会自动被读取。
4. 告诉 Agent：“通过 remote-shell 的 MCP 工具列出已配置服务器”。代理工具模式下，Agent 可以按以下顺序发现并调用：

   ```js
   mcp({ connect: "remote-shell" })
   mcp({ search: "server_list", server: "remote-shell" })
   // 使用搜索返回的实际工具名；默认前缀下通常如下：
   mcp({ tool: "remote_shell_server_list", args: {} })
   ```

以上是 Agent 的工具调用示意，不是在系统终端执行的 JavaScript。若希望全部工具直接出现在模型工具列表中，可在该服务对象中添加 `"directTools": true`，会增加上下文占用。

适配器默认继承 Pi 的进程环境，加密私钥口令应在启动 Pi 前注入。其配置与能力可能随版本变化，以 [适配器说明](https://www.npmjs.com/package/pi-mcp-adapter) 和 [Pi 文档](https://pi.dev) 为准。

## 第一次使用

### 1. 确认工具连接

向 Agent 发送：

> 使用 remote-shell 的 server_list 列出已配置的远程服务器，不执行任何远程命令。

首次返回空列表是正常现象：**MCP 接入成功不代表已经添加服务器，也不代表远程认证成功。**

### 2. 添加目标服务器

例如对 Agent 说：

> 添加名为 dev 的服务器，地址 192.0.2.10，端口 22，用户 deploy，使用凭证引用 dev-key，工作目录 /srv/project。不要自动信任未知主机密钥。

`192.0.2.10` 是文档示例地址，请替换成实际目标。对应的 `server_add` 参数：

```json
{
  "name": "dev",
  "host": "192.0.2.10",
  "port": 22,
  "user": "deploy",
  "credential_ref": "dev-key",
  "workdir": "/srv/project"
}
```

`credential_ref` 必须与本地 `credentials` 的键一致，`workdir` 必须是远端绝对路径。保存返回 `server_id`；重名会拒绝，添加成功只代表配置已持久化，`connection_verified` 仍为 `false`。

### 3. 验证目标，再执行任务

> 查找 dev 服务器，使用它返回的 server_id 执行 `hostname; pwd; id`，确认目标主机、目录和用户。之后只列举工作目录，不修改文件。

对应 `remote_exec` 参数示例（替换 `server_id`）：

```json
{
  "server_id": "srv_替换为实际ID",
  "command": "hostname; pwd; id",
  "timeout_seconds": 30
}
```

确认目标后，可以要求 Agent：

- “读取 dev 的 `logs/app.log` 第 1–100 行。”
- “把 MCP Server 所在机器的 `/absolute/path/build.tar.gz` 上传到 dev 的 `/srv/project/build.tar.gz`，不要覆盖已有文件。”
- “在 dev 新建一个持久化终端，在 `/srv/project` 内运行测试，稍后读取输出；任务结束后先询问我再关闭终端。”

工具选择原则：一次性检查用 `remote_exec`；需要连续 `cd`、设置环境变量或保持长时间进程时，使用 `remote_terminal_create` → `remote_terminal_send` → `remote_terminal_read`。同一 `server_id` 和 `terminal_id` 组合才对应同一会话。

上传下载中的 `local_path` 属于 MCP Server 所在机器，`path` / `remote_path` 属于所选远程服务器。覆盖文件、删除配置或关闭终端前，应核对目标并明确授权。

## 本地日志

所有工具调用（包括未知工具和参数校验失败）通过 slog 保存为本地 JSON Lines 日志，记录开始、结束、call_id、工具名称、server_id、状态和耗时。不记录原始参数、命令、文件内容、输出或错误详情，避免泄漏秘密。success 表示工具调用成功，不代表远程命令退出码为零或终端任务完成。

在 `config.json` 中可配置：

```json
{
  "logging": {
    "directory": "logs",
    "retention_days": 30
  }
}
```

将该字段合并到现有配置，不要替换 `known_hosts` 和 `credentials`。

- 本地指运行 MCP Server 的机器。`directory` 默认为配置文件旁的 `logs` 目录；相对路径基于配置文件目录，也支持绝对路径。修改配置后需重启。
- 按进程本地时区每天一个文件：`remote-shell-mcp-YYYY-MM-DD.jsonl`，同日重启追加写入，不覆盖。
- `retention_days` 默认 30，设为 0 也使用默认值；有效范围 1–36500。保留天数包含今天，例如 7 表示保留今天及前 6 个自然日。
- 启动时及跨天后的首次日志写入时清理过期文件。空闲时不定时清理；仅删除目录内符合上述文件命名且日期有效的过期普通文件，不删除其他文件、目录或符号链接。不同实例应使用独立日志目录。
- 新目录权限为 0700，新日志文件为 0600（受平台及 umask 影响）；已有目录和文件的权限不自动修改。日志按天而非大小轮转，请按调用量预留磁盘空间。
- 日志初始化失败会阻止启动；运行期间写入或轮转失败，在 stderr 告警并输出该条日志，下次写入重试，不静默丢弃。启动/退出诊断仍写 stderr。

## 工具参考

### 服务器

- `server_delete(server_id)`：原子持久化删除本地服务器配置；未知 ID 报错，写入失败保留原配置。不会删除凭证、远程文件或 tmux 会话，不中断已开始的操作；后续调用不能再使用该 ID。
- `server_add(name, host, user, credential_ref, workdir, port?)`：端口默认 22，工作目录必须是远端绝对路径。持久化后返回 `server_id`；不验证连通性，不自动接受主机密钥。重名拒绝。
- `server_list(query?)`：按名称或地址过滤，返回服务器标识和基本信息，不返回认证配置。

添加示例：

```json
{"name":"dev","host":"192.168.1.100","user":"deploy","credential_ref":"dev-key","workdir":"/srv/project"}
```

其他工具必须提供返回的 `server_id`。删除使用 `server_delete`；更新服务器暂未提供工具，可在停止程序后修改配置。

### 命令和文件

| 工具 | 参数（除 server_id 外） |
|---|---|
| `remote_exec` | `command`, `cwd?`, `timeout_seconds?` |
| `remote_read_file` | `path`, `offset?`, `limit?` |
| `remote_write_file` | `path`, `content`, `overwrite?`, `create_parents?` |
| `remote_list_dir` | `path`, `offset?`, `limit?` |
| `remote_upload` | `local_path`, `remote_path`, `overwrite?`, `create_parents?` |
| `remote_download` | `remote_path`, `local_path`, `overwrite?`, `create_parents?` |

- 远端相对路径基于服务器 `workdir`。工作目录不是安全沙箱。
- 本地路径支持绝对路径；相对路径基于 MCP Server 进程工作目录。本地指 MCP Server 所在机器，不是其他环境中的 Agent 客户端。不设置传输根目录限制，访问权限由本地操作系统决定。
- 命令调用相互独立，不分配 PTY，不保留上次调用的 shell 状态。返回 stdout、stderr、退出码、截断、超时和结果未知标记。非零退出码直接体现在结果中。
- 默认命令超时 60 秒，可设 1–600 秒。stdout 和 stderr 各保留最多 1 MiB，之后继续排空但丢弃数据。
- 文本读取最大 10 MiB，UTF-8，不接受 NUL。offset 为 1 起始行号，limit 默认 200、最大 10000。返回的行使用 LF 连接、不保留末尾换行，不用于字节保真复制；保真复制请下载。
- 文本写入最大 10 MiB。创建与覆盖均使用临时文件提交。默认不覆盖、默认不创建父目录。覆盖保留权限位，但不承诺 ACL、扩展属性、硬链接关系或所有权不变。
- 远端无覆盖提交需要 SFTP `hardlink@openssh.com`，覆盖需要 `posix-rename@openssh.com`；不支持则报错，不静默降级。拒绝覆盖最终路径为符号链接的文件。
- 目录 offset 从 0 开始，limit 默认 200、最大 10000；结果含 `next_offset`、`has_more`。目前会在内存读取整个目录，分页不是稳定快照，超大目录优化待实现。
- 上传下载只支持单个普通文件，最大 1 GiB；SFTP 操作总超时 10 分钟。下载通过本地临时文件提交，无覆盖使用硬链接，目标文件系统不支持时会报错。

### 持久化终端

| 工具 | 参数（除 server_id 外） |
|---|---|
| `remote_terminal_create` | `cwd?` |
| `remote_terminal_list` | 无 |
| `remote_terminal_send` | `terminal_id`, `text?`, `submit?`, `key?` |
| `remote_terminal_read` | `terminal_id`, `lines?` |
| `remote_terminal_close` | `terminal_id` |

每台服务器使用隔离的 tmux socket；只操作本项目会话。创建返回 `terminal_id`。输入操作串行化，文本最大 64 KiB。`key` 与文本/submit 互斥，支持 `Enter`、`C-c`、`C-d`、`Tab`、`Escape`。

`send` 只确认发送，不代表命令完成。`read` 捕获屏幕/历史快照，默认向前读取 200 行历史，最大 10000，另含当前屏幕；可能重复且受 tmux 历史容量限制，不提供独立 stdout/stderr/退出码。没有 tmux 服务或会话时，list 当前返回工具错误。

SSH 断开和本地进程退出不主动销毁 tmux 会话，重连后可列举并继续操作。远端重启不保证存活。`close` 会终止其中的任务。tmux 缺失时明确报错，不自动安装。

## 常见问题

| 现象 | 检查方向 |
|---|---|
| 客户端找不到或无法启动程序 | 检查二进制平台、绝对路径、执行权限，以及容器/WSL 是否能访问该路径 |
| `configuration already locked` | 是否还有其他客户端或会话使用同一配置目录？先正常退出旧实例；仅确认无进程运行后才能删除残留 `servers.lock` |
| `known_hosts` 不存在或主机密钥校验失败 | 检查文件路径、主机名及端口；通过可信渠道核对指纹，不要跳过校验或盲目删除旧记录 |
| `unknown credential_ref` | 添加服务器时的引用名必须与 `config.json` 中 `credentials` 的键一致 |
| 私钥口令缺失或解密失败 | 检查私钥而非 `.pub` 文件、口令环境变量及客户端转发规则；Codex 需配置 `env_vars` |
| MCP 已连接，但远程命令失败 | `server_list` 不建立 SSH 连接；继续检查远端地址、防火墙、用户、公钥授权和工作目录是否存在 |
| 文件写入提示不支持或失败 | 检查权限及 SFTP 原子提交扩展；服务端不以不安全的写法静默降级 |
| `tmux_not_found` | 请用户在远端安装 tmux，不是安装到本地；程序不会自动安装 |
| 下一次命令不记得上次 `cd` 的目录 | `remote_exec` 相互独立，每次指定 `cwd`，或改用同一个持久化终端 |
| 长命令/传输提前超时 | 客户端与服务端各有超时；检查客户端工具超时，长任务优先使用持久化终端，不盲目重放已发出的操作 |
| 没看到完整命令或文件内容日志 | 这是设计行为：服务端仅记录调用元数据，避免敏感信息落盘；客户端对话/工具记录仍可能保存原始内容 |

初始化错误查看客户端捕获的 stderr；工具调用记录默认查看配置目录下的 `logs/remote-shell-mcp-YYYY-MM-DD.jsonl`。修改服务端配置后需重启对应 MCP 进程。

## 连接与安全边界

首版每次调用使用独立 SSH 连接，全局最多并发 4 个连接；连接/握手超时 10 秒。这样取消请求可以关闭自己的连接而不影响其他调用。连接复用、保活和可配置限制尚待实现。

请求取消或超时会关闭连接，但不能保证远端子进程终止。已发送的命令、输入、写入不自动重试；结果未知时应先检查实际状态。连接失败绝不回退本地执行。文件传输断线可能遗留 `.remote-shell-*` 临时文件。

提供任意命令执行意味着 Agent 获得该远端账号权限。应使用专用低权限账号，并通过操作系统/容器控制权限。上传下载可访问 MCP Server 进程权限允许的本地路径，请使用低权限本地账号或系统隔离保护私钥等敏感文件。添加服务器允许访问指定网络地址，部署方应通过网络策略限制可访问目标。

## 自动构建与发布

GitHub Actions 工作流位于 `.github/workflows/`：

- `ci.yml`：推送 `main` 或创建/更新 PR 时，在 Linux、macOS、Windows 上检查格式、验证依赖、构建、测试及运行 `go vet`；Linux 额外执行竞态检测。
- `release.yml`：推送严格的 `vMAJOR.MINOR.PATCH` 标签（如 `v0.1.0`）触发，先复用完整 CI，再交叉编译并创建 GitHub Release。也可通过 Actions 页面的 Run workflow 手动运行：只测试和构建六种二进制，产物保存为 Actions artifacts（保留 7 天），不创建或修改 Release。Go 版本取自 `go.mod`，需为 `actions/setup-go` 可下载的版本。

发布步骤（先确保代码已提交并推送）：

```sh
# 示例：选择尚未发布的下一个版本，不要重复使用已有标签
git tag v0.1.1
git push origin v0.1.1
```

发布附件为独立二进制文件，不需要安装 Go，六种平台文件名见[下载二进制](#2-下载二进制)。

同时附带 `LICENSE` 和 `SHA256SUMS`。Linux 可用 `sha256sum --ignore-missing -c SHA256SUMS` 验证下载文件；macOS 可用 `shasum -a 256 <文件>`，Windows 可用 `Get-FileHash <文件> -Algorithm SHA256`，与校验文件对比。Linux/macOS 下载后需 `chmod +x <文件>`；仍需按前文准备连接配置。二进制未做代码签名或 macOS 公证。

构建使用 `CGO_ENABLED=0`；六种目标均交叉编译，但 CI 并未对每种架构都做实机运行验证。发布先创建草稿，附件上传成功后才公开；失败可重新运行工作流以恢复草稿发布。已公开版本不覆盖，请使用新版本标签；不要移动已经发布的标签。只有发布任务拥有仓库写权限，使用内置 `GITHUB_TOKEN`，不需另配令牌。

## 开源协议

本项目采用 [MIT License](LICENSE)，允许任何人使用、修改、分发及商用，须保留版权及许可声明。软件不提供任何担保。第三方依赖遵循各自的许可证。

## 测试与当前限制

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

已提供配置持久化/锁、本地与远端路径解析、输出截断、MCP 工具发现与调用、内存 SSH 服务的认证/主机校验/执行结果测试，以及内存 SFTP 服务的原子写入、禁止覆盖、大小限制与临时文件清理测试。

尚需补充真实 OpenSSH/SFTP/tmux 端到端测试、取消与传输故障注入、大目录流式读取、各平台实机验证。跨平台编译成功不等同于运行验证。原子替换不构成跨进程文件比较交换，不保证阻止远端外部程序并发修改。
