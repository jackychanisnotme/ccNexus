export type DownloadStatus = 'placeholder' | 'unsigned' | 'signed' | 'notarized';

export interface DownloadItem {
  platform: 'macOS' | 'Windows' | 'Linux' | 'Docker';
  arch: string;
  label: string;
  url: string;
  sha256: string;
  status: DownloadStatus;
  notes: string;
}

export interface CommerceProduct {
  id: 'config-pack' | 'node-subscription' | 'deployment-service' | 'api-entry';
  name: string;
  audience: string;
  price: string;
  delivery: string;
  checkoutUrl: string;
  supportUrl: string;
  limits: string;
}

export interface SiteConfig {
  localization: {
    geoEndpoint: string;
    countryCodeField: string;
  };
  release: {
    version: string;
    releaseDate: string;
    status: string;
  };
  links: {
    githubRelease: string;
    dockerDocs: string;
    codexDocs: string;
    claudeDocs: string;
    deploymentDocs: string;
  };
  support: {
    email: string;
    communityUrl: string;
    ticketUrl: string;
  };
  downloads: DownloadItem[];
  commerce: {
    products: CommerceProduct[];
  };
}

export const placeholderUrl = '#contact-sales';

export const siteConfig: SiteConfig = {
  localization: {
    geoEndpoint: 'https://ipapi.co/json/',
    countryCodeField: 'country_code'
  },
  release: {
    version: '6.1.4',
    releaseDate: '待发布',
    status: '仓库可交付版本，真实下载链接在发布后替换'
  },
  links: {
    githubRelease: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
    dockerDocs: 'https://github.com/jackychanisnotme/AINexus/blob/master/docs/README_DOCKER.md',
    codexDocs: 'https://github.com/jackychanisnotme/AINexus#codex-cli',
    claudeDocs: 'https://github.com/jackychanisnotme/AINexus#claude-code',
    deploymentDocs: 'https://github.com/jackychanisnotme/AINexus/blob/master/docs/distribution/deployment-service.md'
  },
  support: {
    email: 'support@example.com',
    communityUrl: '#community-placeholder',
    ticketUrl: '#support-placeholder'
  },
  downloads: [
    {
      platform: 'macOS',
      arch: 'arm64',
      label: 'macOS Apple Silicon',
      url: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
      sha256: '发布后填写',
      status: 'notarized',
      notes: '目标交付为 Developer ID 签名并完成 Apple notarization 的 DMG 或 ZIP。'
    },
    {
      platform: 'macOS',
      arch: 'amd64',
      label: 'macOS Intel',
      url: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
      sha256: '发布后填写',
      status: 'notarized',
      notes: 'Intel 用户下载独立包后按首次打开说明启动。'
    },
    {
      platform: 'Windows',
      arch: 'amd64',
      label: 'Windows x64',
      url: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
      sha256: '发布后填写',
      status: 'unsigned',
      notes: '第一阶段至少提供 x64 便携包；签名证书就绪后升级为签名安装器。'
    },
    {
      platform: 'Windows',
      arch: 'arm64',
      label: 'Windows ARM64',
      url: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
      sha256: '发布后填写',
      status: 'unsigned',
      notes: '作为补充入口，页面明确兼容状态。'
    },
    {
      platform: 'Linux',
      arch: 'amd64',
      label: 'Linux desktop',
      url: 'https://github.com/jackychanisnotme/AINexus/releases/latest',
      sha256: '发布后填写',
      status: 'unsigned',
      notes: '桌面版需满足 WebKit/GTK 依赖；服务器用户优先使用 Docker。'
    },
    {
      platform: 'Docker',
      arch: 'server',
      label: 'Docker / server',
      url: 'https://github.com/jackychanisnotme/AINexus/blob/master/docs/README_DOCKER.md',
      sha256: '镜像 digest 发布后填写',
      status: 'placeholder',
      notes: '用于 VPS、NAS、团队入口和代部署服务。'
    }
  ],
  commerce: {
    products: [
      {
        id: 'config-pack',
        name: '配置包',
        audience: '想快速跑通 Codex CLI、Claude Code 和 OpenAI SDK 的用户',
        price: '29-299 元',
        delivery: '端点模板、客户端配置片段、模型映射和排障说明',
        checkoutUrl: placeholderUrl,
        supportUrl: '#support',
        limits: '不包含上游账号、额度或长期代维护。'
      },
      {
        id: 'node-subscription',
        name: '节点订阅',
        audience: '需要持续更新端点配置源的个人和小团队',
        price: '49-399 元/月',
        delivery: '私有配置文件或订阅链接、节点状态说明和更新记录',
        checkoutUrl: placeholderUrl,
        supportUrl: '#support',
        limits: '第一阶段为人工/半自动更新，不承诺完整自动订阅协议。'
      },
      {
        id: 'deployment-service',
        name: '代部署',
        audience: 'VPS、NAS、软路由、团队服务器用户',
        price: '499 元起',
        delivery: 'Docker/服务器模式部署、Basic Auth、HTTPS、备份和客户端接入',
        checkoutUrl: placeholderUrl,
        supportUrl: '#support',
        limits: '不包含云服务器费用、第三方上游费用和无限售后。'
      },
      {
        id: 'api-entry',
        name: 'API 入口',
        audience: '只想拿到稳定 base_url 和访问凭证的白名单用户',
        price: '99-999 元/月',
        delivery: '专属 base_url、访问凭证、模型列表、额度和禁止用途说明',
        checkoutUrl: placeholderUrl,
        supportUrl: '#support',
        limits: '仅小范围白名单验证，必须接受限流、配额和异常请求封禁。'
      }
    ]
  }
};

export function getCommerceActionLabel(product: CommerceProduct | undefined): string {
  if (!product || product.checkoutUrl === placeholderUrl || product.checkoutUrl.startsWith('#')) {
    return '联系获取';
  }
  return '立即购买';
}

export function getDownloadStatusLabel(status: DownloadStatus): string {
  const labels: Record<DownloadStatus, string> = {
    placeholder: '待发布',
    unsigned: '便携包',
    signed: '已签名',
    notarized: '目标公证'
  };
  return labels[status];
}
