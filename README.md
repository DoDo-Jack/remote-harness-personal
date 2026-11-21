# 远程 Harness · 个人版

把电脑上的 **DeepSeek Harness** 装进手机，**单用户、零注册、零短信**。

这是面向个人自建的版本：没有注册流程、没有短信验证、没有会员和支付。账号密码直接写在服务器的 `config.json` 里，改完重启就能用。

```
手机 ──HTTPS/WS──▶ 你的服务器（Go） ──WSS──▶ 电脑客户端 ──▶ 127.0.0.1:Harness
```

## 和完整版的区别

| | 个人版 | 完整版 |
|---|---|---|
| 账号 | 配置文件里的一组用户名/密码 | 注册 + 短信验证 |
| 会员/支付 | 无 | 有 |
| 服务器地址 | 客户端可改，连自己的服务器 | 固定 |
| 多用户 | 不支持 | 支持 |
| 源码 | 可见，非商业许可 | 否 |

## 三步跑起来

### 1. 启动服务器

```bash
cd server
cp config.example.json config.json
# 编辑 config.json：user / pass / token_secret / listen
go build -o dshhub .
./dshhub
```

`config.json` 示例：

```json
{
  "listen": "127.0.0.1:7100",
  "data_dir": "./data",
  "token_secret": "随便一串随机字符",
  "public_base": "http://127.0.0.1:7100",
  "user": "admin",
  "pass": "你的密码"
}
```

想让手机从外网访问，用 nginx 反代到 `127.0.0.1:7100`，并**透传 WebSocket upgrade**（示例见 `deploy/nginx.conf`）。

### 2. 编译电脑客户端

```bash
cd client-mac
swiftc -O -o RemoteHarness main.swift -framework Cocoa
./RemoteHarness -hub https://你的域名
```

`-hub` 指定服务器地址；不传就默认 `http://127.0.0.1:7100`。也可以改
`~/Library/Application Support/RemoteHarness/config.json` 里的 `hub` 字段。

登录面板里输入你在 `config.json` 设的用户名和密码即可。

### 3. 手机访问

浏览器打开你的域名，用同一组账号登录；想要原生壳，用 `client-ios/` 自己编译（Xcode 项目已带好）。

## 目录

```
server/       Go 服务端（单二进制，仅标准库）
client-mac/   macOS 菜单栏客户端（swiftc 直接编译）
client-ios/   iOS 客户端（SwiftUI + WKWebView）
deploy/       nginx / systemd 示例
```

## 安全提示

- `config.json` 里有密码和签名密钥，**不要提交到仓库、不要发群里**
- 服务端只监听 `127.0.0.1`，对外一律走 nginx + HTTPS
- 客户端保存的登录态是 HMAC 签名 token，有效期一年，换密码后建议换 `token_secret` 让它全部失效

## License

**非商业许可**：允许个人自用、自建、修改；**禁止商业使用**（对外提供服务、企业内部生产使用、转售等）。
商业使用需书面授权（付费）。完整条款见 [LICENSE](LICENSE)。

## 开源与许可

- 源码公开：<https://github.com/DoDo-Jack/remote-harness-personal>
- **非商业许可**：允许个人自用、自建、修改；**禁止商业使用**（对外提供服务、企业内部生产使用、转售等）。
- 商业使用需书面授权（付费）。条款见 [LICENSE](LICENSE)。

## 配置说明

服务器配置在 `server/config.json`（首次运行自动生成，或复制 `config.example.json`）：

| 字段 | 说明 |
|---|---|
| `listen` | 监听地址，默认 `127.0.0.1:7100`，对外由 nginx 反代 |
| `data_dir` | 数据目录（设备、登录态） |
| `token_secret` | 签发登录态的密钥，**换它等于让所有登录失效** |
| `user` / `pass` | 登录账号密码（个人版只有这一组） |
| `public_base` | 对外地址，用于回调/链接 |

改完配置执行 `systemctl restart dshhub`（或重启你的进程）即可生效；客户端不用重装。
