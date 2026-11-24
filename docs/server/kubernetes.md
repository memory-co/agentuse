# Kubernetes 部署

AgentUse Server 镜像支持 Linux amd64 和 arm64，包含管理网页以及 Linux 和 macOS 的 amd64、arm64 Client。设备注册命令仍从 Server 获取 `setup.sh` 和对应的 Client 安装包。

GitHub Actions 将镜像发布到 `ghcr.io/memory-co/agentuse-server`。默认使用随仓库更新的 `:main`。也可固定到已发布的版本标签或 `:sha-<完整提交哈希>`。私有镜像需要在部署命名空间配置 `imagePullSecrets`。

## 安装

需要 Kubernetes 1.23+ 及可用的默认 StorageClass，或自行提供可绑定的卷。先克隆仓库，再从仓库根目录执行下列命令之一。

### 原生 YAML

编辑 `deploy/kubernetes.yaml` 中的镜像标签、PVC 大小或 `storageClassName`。如需接入远程设备，将 `AGENTUSE_PUBLIC_URL` 设置为设备可访问的地址。

```bash
kubectl create namespace agentuse
kubectl apply -n agentuse -f deploy/kubernetes.yaml
```

### Helm

```bash
helm upgrade --install agentuse ./deploy/helm/agentuse \
  --namespace agentuse --create-namespace
```

常用参数见 [values.yaml](../../deploy/helm/agentuse/values.yaml)。下面的示例开启 Ingress，并使用命名空间内已有的 TLS Secret。

```yaml
ingress:
  enabled: true
  className: nginx
  host: agentuse.example.com
  tlsSecretName: agentuse-tls
persistence:
  size: 20Gi
```

将示例保存为 `values.agentuse.yaml`，安装或更新时加上 `-f values.agentuse.yaml`。如果 TLS 在 Ingress 之前终止，或通过 LoadBalancer、其他反向代理对外提供服务，可显式设置 `publicURL`。反向代理应转发根路径下的 HTTP 请求和 WebSocket Upgrade，允许设备和浏览器维持长连接。

### 登录

```bash
kubectl -n agentuse rollout status deploy/agentuse
kubectl -n agentuse logs deploy/agentuse
kubectl -n agentuse port-forward svc/agentuse 8080:8080
```

首次启动日志包含管理员登录链接。默认公开地址为 `http://localhost:8080`，适合本机转发测试。实际接入设备时使用配置的公开地址登录，再复制设备注册命令。

## 镜像拉取权限

GHCR 初次发布时默认将镜像包设为私有。包管理员可以在 GitHub 的 Package settings 中将可见性设为 Public，允许集群匿名拉取。[GitHub Packages 可见性说明](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)

对私有镜像，先在部署命名空间创建镜像拉取 Secret。GHCR 凭据需有 `read:packages` 和对应包的读取权限。将 Secret 名称填入 Helm 的 `imagePullSecrets`，例如 `[{name: ghcr}]`。使用原生 YAML 时填在 Deployment 的 `spec.template.spec.imagePullSecrets`。

## 存储与更新

- 部署使用单副本和 `Recreate`，更新期间旧 Pod 先退出，随后新 Pod 接管。同一 PVC 保存 `/data/.agentuse` 下的 SQLite 数据库、附件、Skill 文件及设备证书和签名材料。备份和迁移时应一起保留这些数据。
- Helm 支持通过 `persistence.existingClaim` 使用已有 PVC。Chart 创建的 PVC 在 Helm 卸载时保留，重装可引用该 PVC。使用原生 YAML 时，如需保留数据，只删除 Deployment 和 Service。
- 可通过 Helm 的 `envFrom` 或原生 Deployment 的 `envFrom` 引用已有 Secret 或 ConfigMap，配置 `AGENTUSE_DB_DRIVER`、`AGENTUSE_DB` 或 S3 文件后端。即使数据库及文件都外置，仍需保留 PVC 上的设备证书及签名材料。[数据库配置](database.md) · [S3 存储](cli.md#s3-存储)
- 使用原生 YAML 时，修改镜像标签后重新 `kubectl apply`。使用 Helm 时，带上现有 values 文件，更新 `image.tag` 后执行 `helm upgrade`。如果继续使用 `:main`，镜像更新后用 `kubectl -n agentuse rollout restart deploy/agentuse` 重新拉取。

## 自行构建

```bash
docker build --build-arg VERSION="$(git describe --tags --always)" -t agentuse-server:local .
```

使用自建仓库时，推送镜像后在原生 YAML 中替换 `image`，或设置 Helm 的 `image.repository` 和 `image.tag`。[镜像构建工作流](../../.github/workflows/container.yml)
