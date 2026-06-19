export type Locale = 'zh-CN' | 'en-US';

export interface Messages {
  nav: {
    download: string;
    docs: string;
    pricing: string;
    support: string;
    partners: string;
    contact: string;
    language: string;
  };
  hero: {
    kicker: string;
    title: string;
    text: string;
    mac: string;
    windows: string;
    phase: string;
  };
  stats: {
    version: string;
    products: string;
    period: string;
  };
  sections: {
    downloadTitle: string;
    downloadText: string;
    docsTitle: string;
    docsText: string;
    pricingTitle: string;
    pricingText: string;
    supportTitle: string;
    supportText: string;
    partnersTitle: string;
    partnersText: string;
  };
  labels: {
    version: string;
    sha256: string;
    getEntry: string;
    viewGuide: string;
    submitTicket: string;
    emailSupport: string;
    registerPartner: string;
  };
  aria: {
    home: string;
    primaryNav: string;
    phaseSummary: string;
  };
}

export const chineseIpCountries = new Set(['CN', 'HK', 'MO', 'TW']);
export const defaultLocale: Locale = 'en-US';

export const messages: Record<Locale, Messages> = {
  'zh-CN': {
    nav: {
      download: '下载',
      docs: '教程',
      pricing: '付费',
      support: '支持',
      partners: '渠道',
      contact: '联系获取',
      language: 'English'
    },
    hero: {
      kicker: 'AINexus v6.1.4 第一阶段分发验证',
      title: '统一 AI API Provider',
      text: '面向 Codex CLI、Claude Code 与 OpenAI 兼容工具，本地或服务器部署。',
      mac: '下载 macOS',
      windows: '下载 Windows',
      phase: '第一阶段'
    },
    stats: {
      version: '当前验证基线',
      products: '付费产品假设',
      period: '真实付费验证周期'
    },
    sections: {
      downloadTitle: '多平台下载入口',
      downloadText: '链接和校验值集中在配置文件里，发布后替换占位，不改页面结构。',
      docsTitle: '先让用户跑通一次请求',
      docsText: '教程围绕 Codex CLI、Claude Code、OpenAI SDK 和 Docker 服务器模式，不把用户推向复杂后台。',
      pricingTitle: '四类付费验证产品',
      pricingText: '第一版承接真实付款意向，但交付仍保持人工或半自动。',
      supportTitle: '支持入口与 30 天验证',
      supportText: '支持页承接 FAQ、排障、远程协助和订单反馈。每天记录访问、下载、咨询、订单、收入、退款和跑通请求人数。',
      partnersTitle: '渠道合作只收集意向',
      partnersText: '教程作者、社群群主和部署服务商可以先进入转介绍流程，第一阶段不建设分销系统。'
    },
    labels: {
      version: '版本',
      sha256: 'SHA256',
      getEntry: '获取入口',
      viewGuide: '查看教程',
      submitTicket: '提交工单',
      emailSupport: '邮件支持',
      registerPartner: '登记合作'
    },
    aria: {
      home: 'AINexus 首页',
      primaryNav: '主导航',
      phaseSummary: '第一阶段交付摘要'
    }
  },
  'en-US': {
    nav: {
      download: 'Download',
      docs: 'Guides',
      pricing: 'Pricing',
      support: 'Support',
      partners: 'Partners',
      contact: 'Contact',
      language: '中文'
    },
    hero: {
      kicker: 'AINexus v6.1.4 phase-one distribution',
      title: 'Unified AI API Provider',
      text: 'One local or server entrypoint for Codex CLI, Claude Code, and OpenAI-compatible tools.',
      mac: 'Download macOS',
      windows: 'Download Windows',
      phase: 'Phase one'
    },
    stats: {
      version: 'Validation baseline',
      products: 'Paid product bets',
      period: 'Paid validation window'
    },
    sections: {
      downloadTitle: 'Multi-platform downloads',
      downloadText: 'Download links and checksums live in one config file, ready to replace after release.',
      docsTitle: 'Get users to one successful request',
      docsText: 'Guides focus on Codex CLI, Claude Code, OpenAI SDK, and Docker server mode.',
      pricingTitle: 'Four paid validation products',
      pricingText: 'The first version captures real buying intent while delivery remains manual or semi-automated.',
      supportTitle: 'Support and 30-day validation',
      supportText: 'Support collects FAQ, troubleshooting, remote help, and order feedback while tracking daily conversion data.',
      partnersTitle: 'Partner interest only',
      partnersText: 'Tutorial authors, community owners, and deployment partners can enter a referral flow without a full affiliate system.'
    },
    labels: {
      version: 'Version',
      sha256: 'SHA256',
      getEntry: 'Get entry',
      viewGuide: 'View guide',
      submitTicket: 'Submit ticket',
      emailSupport: 'Email support',
      registerPartner: 'Register'
    },
    aria: {
      home: 'AINexus home',
      primaryNav: 'Primary navigation',
      phaseSummary: 'Phase-one delivery summary'
    }
  }
};

export function isLocale(value: string): value is Locale {
  return value === 'zh-CN' || value === 'en-US';
}

export function resolveLocaleFromCountry(countryCode: string | null | undefined): Locale {
  const normalized = String(countryCode || '').trim().toUpperCase();
  return chineseIpCountries.has(normalized) ? 'zh-CN' : 'en-US';
}

export function resolveLocaleFromNavigator(languages: readonly string[]): Locale {
  const first = languages.find(Boolean) || '';
  return first.toLowerCase().startsWith('zh') ? 'zh-CN' : defaultLocale;
}

export function nextLocale(locale: Locale): Locale {
  return locale === 'zh-CN' ? 'en-US' : 'zh-CN';
}
