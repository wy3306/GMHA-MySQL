# GMHA 完整程序包（Linux x86_64）

此程序包不要求目标机器安装 Go 或 Node.js。

## 一键启动

```bash
chmod +x start-web.sh gmha-web gmha bin/agentd
./start-web.sh
```

浏览器访问：

```text
http://服务器IP:8079
```

在“GMHA 启动中心”点击“启动 Manager”，健康检查通过后点击“进入 GMHA 控制台”。

## 程序包内容

- `gmha-web`：轻量 Web 启动器，默认监听 `0.0.0.0:8079`
- `gmha`：Manager、业务 API 和内嵌 Web 控制台
- `bin/agentd`：由 Manager 部署到受管服务器的 Agent
- `data/`：默认 SQLite 数据目录
- `logs/`：Manager 日志目录
- `start-web.sh`：一键启动脚本

Manager 默认监听 HTTP `:8080` 和 gRPC `:9100`。请在防火墙中放通 8079、8080、9100 端口。

如果浏览器访问 Manager 需要使用指定域名或 IP：

```bash
GMHA_MANAGER_URL=http://gmha.example.com:8080 ./start-web.sh
```

如果 Manager SSH 密钥不在运行用户的默认 `~/.ssh/id_ed25519` 或
`~/.ssh/id_rsa` 路径，可指定公钥路径；启动器会同时使用对应的私钥验证已有互信：

```bash
GMHA_MANAGER_PUBKEY=/opt/gmha/manager_ed25519.pub ./start-web.sh
```

数据库默认保存到程序包内的 `data/manager.db`，Manager 日志保存到 `logs/manager.log`。

## 版本升级

Manager 和 Agent 的初始版本均为 `V0.0.1`。执行 `scripts/build-release.sh V0.0.2`
会在 `dist/` 额外生成可直接上传到 Web 控制台的两个升级制品：

- `gmha-manager-V0.0.2-linux-amd64.bin`：上传到 `GMHA Manager` 分类。
- `gmha-agent-V0.0.2-linux-amd64.bin`：上传到 `GMHA Agent` 分类。

上传后进入“平台运维 → 版本升级”。Manager 升级会校验候选版本、备份当前程序、
原子替换并重启；Agent 升级会检查在线状态与架构，逐台备份替换，并以新鲜心跳上报的
版本作为升级后检查结果。升级记录与各阶段结果保存在 `~/.gmha/upgrade-jobs.json`。
