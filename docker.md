## 运行客户端
```bash
docker run --name meseproxy   -p 1080:1080  -p 19080:19080  -p 4001:4001/tcp -p 4001:4001/udp --restart unless-stopped    --restart unless-stopped  -v  meshproxy:/app/data -d  chenjia404/meshproxy:dev
```
节点数据挂载在卷meshproxy上，1080端口是socks5的代理端口，4001是p2p网络端口，19080是控制台网页端口。

## 运行服务端
```bash
docker run --name meseproxy   -p 4001:4001/tcp -p 4001:4001/udp --restart unless-stopped    --restart unless-stopped  -v  meshproxy:/app/data -d  chenjia404/meshproxy:dev -mode relay+exit
```
服务器只需要打开4001端口即可，如果你需要控制台可以打开19080，注意目前还没有支持控制台设置密码功能。


## 端口冲突

如果你的1080端口被占用，你可以修改为 -p 1082:1080 ，修改左边的端口即可。