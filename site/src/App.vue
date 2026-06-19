<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue';
import {
  messages,
  nextLocale,
  resolveLocaleFromCountry,
  resolveLocaleFromNavigator,
  isLocale,
  type Locale
} from './i18n';
import {
  getCommerceActionLabel,
  siteConfig,
  type CommerceProduct,
  type DownloadItem,
  type DownloadStatus
} from './site.config';

type HeroPanelId = 'desktop' | 'commerce' | 'server';

const locale = ref<Locale>(resolveLocaleFromNavigator(globalThis.navigator?.languages || []));
const activeHeroPanelId = ref<HeroPanelId>('desktop');
const activeDownloadKey = ref('macOS-arm64');
const activeGuideIndex = ref(0);
const activeProductId = ref<CommerceProduct['id']>('config-pack');
const isMenuOpen = ref(false);

const t = computed(() => messages[locale.value]);

const ui = computed(() =>
  locale.value === 'zh-CN'
    ? {
        menu: '菜单',
        close: '关闭',
        heroSecondary: '查看付费方案',
        toolLabel: '为这些工具准备',
        releaseLabel: '发布状态',
        quickTitle: '第一阶段闭环',
        downloadSelector: '选择下载入口',
        selectedBuild: '当前选择',
        allBuilds: '全部交付入口',
        guideSelector: '选择教程',
        commandLabel: '验证命令',
        productSelector: '选择产品',
        productDelivery: '交付内容',
        productLimits: '边界',
        validationLabel: '30 天验证看板',
        supportCta: '需要人工协助',
        partnerCta: '渠道合作',
        previewAlt: 'AINexus 桌面界面预览',
        packageLabel: '交付包',
        handoffLabel: '交付路径',
        handoffReady: '可人工交付',
        dashboardLabel: '验证看板',
        releaseDate: '发布时间'
      }
    : {
        menu: 'Menu',
        close: 'Close',
        heroSecondary: 'See paid products',
        toolLabel: 'Ready for',
        releaseLabel: 'Release status',
        quickTitle: 'Phase-one loop',
        downloadSelector: 'Choose an entry',
        selectedBuild: 'Selected build',
        allBuilds: 'All delivery entries',
        guideSelector: 'Choose a guide',
        commandLabel: 'Validation command',
        productSelector: 'Choose a product',
        productDelivery: 'Delivery',
        productLimits: 'Boundary',
        validationLabel: '30-day validation board',
        supportCta: 'Need human support',
        partnerCta: 'Partner interest',
        previewAlt: 'AINexus desktop interface preview',
        packageLabel: 'Package',
        handoffLabel: 'Delivery path',
        handoffReady: 'Ready for manual fulfillment',
        dashboardLabel: 'Validation board',
        releaseDate: 'Release date'
      }
);

const navItems = computed(() => [
  { href: '#download', label: t.value.nav.download },
  { href: '#docs', label: t.value.nav.docs },
  { href: '#pricing', label: t.value.nav.pricing },
  { href: '#support', label: t.value.nav.support },
  { href: '#partners', label: t.value.nav.partners }
]);

const heroStats = computed(() => [
  { value: '6.1.4', label: t.value.stats.version },
  { value: '4', label: t.value.stats.products },
  { value: locale.value === 'zh-CN' ? '30 天' : '30 days', label: t.value.stats.period }
]);

const toolChips = computed(() =>
  locale.value === 'zh-CN'
    ? ['Codex CLI', 'Claude Code', 'OpenAI SDK', 'Docker', 'WebUI']
    : ['Codex CLI', 'Claude Code', 'OpenAI SDK', 'Docker', 'WebUI']
);

const heroPanels = computed(() =>
  locale.value === 'zh-CN'
    ? [
        {
          id: 'desktop' as const,
          label: '下载',
          title: 'macOS 与 Windows 首屏明确交付',
          text: '优先独立站分发：macOS 走签名/公证流程，Windows 作为第一阶段核心入口。',
          metric: 'macOS + Windows',
          href: '#download',
          cta: t.value.labels.getEntry
        },
        {
          id: 'commerce' as const,
          label: '付费',
          title: '四类产品验证真实付款',
          text: '配置包、节点订阅、代部署、API 入口都保持可解释、可交付、可复盘。',
          metric: '4 个 SKU',
          href: '#pricing',
          cta: t.value.nav.contact
        },
        {
          id: 'server' as const,
          label: '部署',
          title: '复用服务器模式与 Docker',
          text: '用现有 WebUI、端点管理、统计和备份能力承接团队与代部署需求。',
          metric: 'Docker / WebUI',
          href: '#docs',
          cta: t.value.labels.viewGuide
        }
      ]
    : [
        {
          id: 'desktop' as const,
          label: 'Download',
          title: 'macOS and Windows are first-class entries',
          text: 'The standalone site leads distribution: macOS targets signed/notarized builds, Windows ships in phase one.',
          metric: 'macOS + Windows',
          href: '#download',
          cta: t.value.labels.getEntry
        },
        {
          id: 'commerce' as const,
          label: 'Commerce',
          title: 'Four paid products validate real buying intent',
          text: 'Config packs, node subscriptions, managed deployment, and API entry stay clear, deliverable, and reviewable.',
          metric: '4 SKUs',
          href: '#pricing',
          cta: t.value.nav.contact
        },
        {
          id: 'server' as const,
          label: 'Deploy',
          title: 'Server mode and Docker do the heavy lifting',
          text: 'Reuse WebUI, endpoint management, stats, and backups for team and managed-deployment demand.',
          metric: 'Docker / WebUI',
          href: '#docs',
          cta: t.value.labels.viewGuide
        }
      ]
);

const activeHeroPanel = computed(
  () => heroPanels.value.find((panel) => panel.id === activeHeroPanelId.value) || heroPanels.value[0]
);

const previewImage = computed(() =>
  locale.value === 'zh-CN' ? '/product/desktop-dark-cn.png' : '/product/desktop-dark-en.png'
);

const previewBadges = computed(() =>
  locale.value === 'zh-CN'
    ? ['端点轮换', '协议转换', '统计', '备份']
    : ['Endpoint rotation', 'Protocol conversion', 'Stats', 'Backups']
);

const quickPaths = computed(() =>
  locale.value === 'zh-CN'
    ? [
        { title: '下载', text: '按平台拿到独立包', href: '#download' },
        { title: '教程', text: '把一次请求跑通', href: '#docs' },
        { title: '付费', text: '验证四类产品', href: '#pricing' },
        { title: '支持', text: '收敛售后问题', href: '#support' }
      ]
    : [
        { title: 'Download', text: 'Pick a standalone build', href: '#download' },
        { title: 'Guides', text: 'Complete one request', href: '#docs' },
        { title: 'Pricing', text: 'Validate four products', href: '#pricing' },
        { title: 'Support', text: 'Control support load', href: '#support' }
      ]
);

const tutorials = computed(() =>
  locale.value === 'zh-CN'
    ? [
        {
          title: 'Codex CLI 接入',
          text: '使用 OpenAI Responses API 路径，把 Codex CLI 指向 AINexus 的 /v1 入口。',
          link: siteConfig.links.codexDocs
        },
        {
          title: 'Claude Code 接入',
          text: '使用 Claude/Anthropic 兼容入口，让 Claude Code 通过 AINexus 管理上游。',
          link: siteConfig.links.claudeDocs
        },
        {
          title: 'Docker 服务器模式',
          text: '用 docker compose 跑起无头代理、WebUI、Basic Auth 和持久化数据目录。',
          link: siteConfig.links.dockerDocs
        }
      ]
    : [
        {
          title: 'Codex CLI setup',
          text: 'Point Codex CLI at the AINexus /v1 entrypoint with the OpenAI Responses API path.',
          link: siteConfig.links.codexDocs
        },
        {
          title: 'Claude Code setup',
          text: 'Use the Claude-compatible entrypoint so Claude Code can route through AINexus.',
          link: siteConfig.links.claudeDocs
        },
        {
          title: 'Docker server mode',
          text: 'Run the headless proxy, WebUI, Basic Auth, and persistent data directory with docker compose.',
          link: siteConfig.links.dockerDocs
        }
      ]
);

const guideCards = computed(() => {
  const commands = ['codex --model gpt-5-codex', 'ANTHROPIC_BASE_URL=http://127.0.0.1:3000', 'docker compose up -d'];
  const badges =
    locale.value === 'zh-CN'
      ? ['Responses API', 'Claude 兼容入口', '服务器模式']
      : ['Responses API', 'Claude-compatible', 'Server mode'];

  return tutorials.value.map((guide, index) => ({
    ...guide,
    step: String(index + 1).padStart(2, '0'),
    command: commands[index],
    badge: badges[index]
  }));
});

const activeGuide = computed(() => guideCards.value[activeGuideIndex.value] || guideCards.value[0]);

const validationMetrics = computed(() =>
  locale.value === 'zh-CN'
    ? [
        'macOS notarized 包和 Windows x64 包可下载',
        '至少 2 篇核心教程发布并能跑通请求',
        '至少 3 单真实付费，理想目标 20 单以上',
        'API 入口毛利为正，异常请求可控'
      ]
    : [
        'macOS notarized package and Windows x64 package are downloadable',
        'At least two core guides are published and can complete a request',
        'At least three real paid orders, with 20+ as the ideal target',
        'API entry has positive gross margin and controllable abuse'
      ]
);

const productCopy = computed<Record<CommerceProduct['id'], Pick<CommerceProduct, 'name' | 'audience' | 'delivery' | 'limits'>>>(
  () =>
    locale.value === 'zh-CN'
      ? Object.fromEntries(
          siteConfig.commerce.products.map((product) => [
            product.id,
            {
              name: product.name,
              audience: product.audience,
              delivery: product.delivery,
              limits: product.limits
            }
          ])
        ) as Record<CommerceProduct['id'], Pick<CommerceProduct, 'name' | 'audience' | 'delivery' | 'limits'>>
      : {
          'config-pack': {
            name: 'Config Pack',
            audience: 'For users who want Codex CLI, Claude Code, and OpenAI SDK running quickly',
            delivery: 'Endpoint templates, client snippets, model mapping, and troubleshooting notes',
            limits: 'Does not include upstream accounts, API credits, or long-term maintenance.'
          },
          'node-subscription': {
            name: 'Node Subscription',
            audience: 'For individuals and small teams that need a maintained endpoint configuration source',
            delivery: 'Private config file or subscription link, node status notes, and update logs',
            limits: 'Phase one is manual or semi-automated and does not promise a full subscription protocol.'
          },
          'deployment-service': {
            name: 'Managed Deployment',
            audience: 'For VPS, NAS, router, and team server users',
            delivery: 'Docker/server setup, Basic Auth, HTTPS, backups, and client integration',
            limits: 'Does not include cloud server fees, upstream fees, or unlimited support.'
          },
          'api-entry': {
            name: 'API Entry',
            audience: 'For allowlisted users who only need a stable base_url and access credential',
            delivery: 'Dedicated base_url, credential, model list, quota, and acceptable-use rules',
            limits: 'Small allowlist only, with rate limits, quotas, and abuse blocking.'
          }
        }
);

const productDeck = computed(() =>
  siteConfig.commerce.products.map((product, index) => ({
    product,
    copy: copyFor(product),
    price: priceFor(product),
    index: index + 1
  }))
);

const activeProduct = computed(
  () => productDeck.value.find((item) => item.product.id === activeProductId.value) || productDeck.value[0]
);

const downloadNotes = computed<Record<string, string>>(() =>
  locale.value === 'zh-CN'
    ? Object.fromEntries(siteConfig.downloads.map((item) => [`${item.platform}-${item.arch}`, item.notes]))
    : {
        'macOS-arm64': 'Target delivery is a Developer ID signed and Apple-notarized DMG or ZIP.',
        'macOS-amd64': 'Intel users download the standalone package and follow the first-open instructions.',
        'Windows-amd64': 'Phase one ships at least an x64 portable package; signed installer follows when certificates are ready.',
        'Windows-arm64': 'Supplemental entry with compatibility status clearly labeled.',
        'Linux-amd64': 'Desktop builds require WebKit/GTK dependencies; server users should prefer Docker.',
        'Docker-server': 'For VPS, NAS, team API entry, and managed deployment service.'
      }
);

const downloadOptions = computed(() =>
  siteConfig.downloads.map((item) => ({
    item,
    key: `${item.platform}-${item.arch}`,
    note: downloadNotes.value[`${item.platform}-${item.arch}`]
  }))
);

const activeDownload = computed(
  () => downloadOptions.value.find((option) => option.key === activeDownloadKey.value) || downloadOptions.value[0]
);

const activeDownloadVisual = computed(() => {
  const item = activeDownload.value.item;
  const styleByPlatform: Record<DownloadItem['platform'], string> = {
    macOS: 'visual-macos',
    Windows: 'visual-windows',
    Linux: 'visual-linux',
    Docker: 'visual-docker'
  };

  return {
    className: styleByPlatform[item.platform],
    platform: item.platform,
    arch: item.arch,
    status: statusFor(item.status)
  };
});

const primaryDownload = computed(() => siteConfig.downloads.find((item) => item.platform === 'macOS'));
const releaseStatus = computed(() =>
  locale.value === 'zh-CN'
    ? siteConfig.release.status
    : 'Repository-ready version; live download links will be replaced after release'
);

function copyFor(product: CommerceProduct): Pick<CommerceProduct, 'name' | 'audience' | 'delivery' | 'limits'> {
  return productCopy.value[product.id];
}

function priceFor(product: CommerceProduct): string {
  if (locale.value === 'zh-CN') {
    return product.price;
  }

  const prices: Record<CommerceProduct['id'], string> = {
    'config-pack': 'RMB 29-299',
    'node-subscription': 'RMB 49-399 / month',
    'deployment-service': 'From RMB 499',
    'api-entry': 'RMB 99-999 / month'
  };
  return prices[product.id];
}

function statusFor(status: DownloadStatus): string {
  const zh: Record<DownloadStatus, string> = {
    placeholder: '待发布',
    unsigned: '便携包',
    signed: '已签名',
    notarized: '目标公证'
  };
  const en: Record<DownloadStatus, string> = {
    placeholder: 'Pending',
    unsigned: 'Portable',
    signed: 'Signed',
    notarized: 'Notarized target'
  };
  return locale.value === 'zh-CN' ? zh[status] : en[status];
}

function checksumFor(item: DownloadItem): string {
  if (locale.value === 'zh-CN') {
    return item.sha256;
  }
  return item.platform === 'Docker' ? 'Image digest after release' : 'To be filled after release';
}

function readSavedLocale(): Locale | null {
  try {
    const value = globalThis.localStorage?.getItem?.('ainexus-site-locale');
    return value && isLocale(value) ? value : null;
  } catch {
    return null;
  }
}

function saveLocale(value: Locale): void {
  try {
    globalThis.localStorage?.setItem?.('ainexus-site-locale', value);
  } catch {
    // Storage may be disabled in private or embedded browser contexts.
  }
}

function productAction(product: CommerceProduct): string {
  if (getCommerceActionLabel(product) === '立即购买') {
    return locale.value === 'zh-CN' ? '立即购买' : 'Buy now';
  }
  return t.value.nav.contact;
}

function switchLanguage(): void {
  locale.value = nextLocale(locale.value);
  saveLocale(locale.value);
  isMenuOpen.value = false;
}

async function initializeLocale(): Promise<void> {
  if (import.meta.env.MODE === 'test') {
    return;
  }

  const savedLocale = readSavedLocale();
  if (savedLocale) {
    locale.value = savedLocale;
    return;
  }

  try {
    const response = await fetch(siteConfig.localization.geoEndpoint, {
      signal: AbortSignal.timeout(1800)
    });
    if (!response.ok) {
      throw new Error(`Geo lookup failed: ${response.status}`);
    }
    const payload = (await response.json()) as Record<string, unknown>;
    locale.value = resolveLocaleFromCountry(String(payload[siteConfig.localization.countryCodeField] || ''));
  } catch {
    locale.value = resolveLocaleFromNavigator(globalThis.navigator?.languages || []);
  }
}

watchEffect(() => {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = locale.value === 'zh-CN' ? 'zh-CN' : 'en';
  }
});

onMounted(() => {
  void initializeLocale();
});
</script>

<template>
  <div class="site-shell">
    <header class="nav" :class="{ 'nav-open': isMenuOpen }">
      <a class="brand" href="#home" :aria-label="t.aria.home" @click="isMenuOpen = false">
        <span class="brand-mark">Ai</span>
        <span class="brand-copy">
          <strong>AINexus</strong>
          <small>{{ ui.toolLabel }}</small>
        </span>
      </a>

      <button
        class="menu-button"
        type="button"
        :aria-expanded="isMenuOpen"
        :aria-controls="'primary-navigation'"
        @click="isMenuOpen = !isMenuOpen"
      >
        {{ isMenuOpen ? ui.close : ui.menu }}
      </button>

      <nav id="primary-navigation" class="nav-links" :aria-label="t.aria.primaryNav">
        <a v-for="item in navItems" :key="item.href" :href="item.href" @click="isMenuOpen = false">{{ item.label }}</a>
      </nav>

      <div class="nav-actions">
        <button class="language-button" type="button" @click="switchLanguage">{{ t.nav.language }}</button>
        <a class="nav-action" href="#pricing" @click="isMenuOpen = false">{{ t.nav.contact }}</a>
      </div>
    </header>

    <main>
      <section id="home" class="hero section">
        <div class="hero-copy">
          <p class="kicker">{{ t.hero.kicker }}</p>
          <h1>{{ t.hero.title }}</h1>
          <p class="hero-text">{{ t.hero.text }}</p>
          <div class="hero-actions">
            <a class="button primary" :href="primaryDownload?.url || '#download'">{{ t.hero.mac }}</a>
            <a class="button secondary" href="#download">{{ t.hero.windows }}</a>
          </div>
        </div>

        <aside class="spotlight-stage" :aria-label="t.aria.phaseSummary">
          <div class="spotlight-tabs">
            <button
              v-for="panel in heroPanels"
              :key="panel.id"
              class="spotlight-tab"
              :class="{ active: activeHeroPanelId === panel.id }"
              type="button"
              :aria-pressed="activeHeroPanelId === panel.id"
              @click="activeHeroPanelId = panel.id"
            >
              {{ panel.label }}
            </button>
          </div>

          <div class="spotlight-card">
            <span>{{ activeHeroPanel.metric }}</span>
            <h2>{{ activeHeroPanel.title }}</h2>
            <p>{{ activeHeroPanel.text }}</p>
            <a :href="activeHeroPanel.href">{{ activeHeroPanel.cta }}</a>
          </div>

          <figure class="product-preview">
            <img :src="previewImage" :alt="ui.previewAlt" />
            <figcaption>
              <span v-for="badge in previewBadges" :key="badge">{{ badge }}</span>
            </figcaption>
          </figure>

          <div class="release-panel">
            <span>{{ ui.releaseLabel }}</span>
            <strong>{{ releaseStatus }}</strong>
          </div>
        </aside>
      </section>

      <section class="proof-strip" aria-label="AINexus phase-one proof points">
        <div v-for="stat in heroStats" :key="stat.label" class="proof-item">
          <strong>{{ stat.value }}</strong>
          <span>{{ stat.label }}</span>
        </div>
        <div class="tool-strip" :aria-label="ui.toolLabel">
          <span v-for="tool in toolChips" :key="tool">{{ tool }}</span>
        </div>
      </section>

      <section class="quick-paths" :aria-label="ui.quickTitle">
        <a v-for="path in quickPaths" :key="path.href" :href="path.href" class="quick-path">
          <strong>{{ path.title }}</strong>
          <span>{{ path.text }}</span>
        </a>
      </section>

      <section id="download" class="section download-lab">
        <div class="section-heading">
          <h2>{{ t.sections.downloadTitle }}</h2>
          <p>{{ t.sections.downloadText }}</p>
        </div>

        <div class="download-showcase">
          <div class="selector-panel">
            <span>{{ ui.downloadSelector }}</span>
            <button
              v-for="option in downloadOptions"
              :key="option.key"
              class="selector-button"
              :class="{ active: activeDownloadKey === option.key }"
              type="button"
              :aria-pressed="activeDownloadKey === option.key"
              @click="activeDownloadKey = option.key"
            >
              <strong>{{ option.item.label }}</strong>
              <small>{{ statusFor(option.item.status) }}</small>
            </button>
          </div>

          <article class="selected-download">
            <div>
              <span class="status-pill">{{ ui.selectedBuild }}</span>
              <h3>{{ activeDownload.item.label }}</h3>
              <p>{{ activeDownload.note }}</p>
            </div>
            <div class="download-visual" :class="activeDownloadVisual.className">
              <div class="package-window">
                <span>{{ ui.packageLabel }}</span>
                <strong>{{ activeDownloadVisual.platform }}</strong>
                <small>{{ activeDownloadVisual.arch }} / {{ activeDownloadVisual.status }}</small>
              </div>
              <div class="delivery-track">
                <span>{{ ui.handoffLabel }}</span>
                <strong>{{ ui.handoffReady }}</strong>
              </div>
            </div>
            <div>
              <dl class="meta-grid">
                <div>
                  <dt>{{ t.labels.version }}</dt>
                  <dd>{{ siteConfig.release.version }}</dd>
                </div>
                <div>
                  <dt>{{ t.labels.sha256 }}</dt>
                  <dd>{{ checksumFor(activeDownload.item) }}</dd>
                </div>
                <div>
                  <dt>Status</dt>
                  <dd>{{ statusFor(activeDownload.item.status) }}</dd>
                </div>
                <div>
                  <dt>{{ ui.releaseDate }}</dt>
                  <dd>{{ siteConfig.release.releaseDate }}</dd>
                </div>
              </dl>
              <a class="button primary" :href="activeDownload.item.url">{{ t.labels.getEntry }}</a>
            </div>
          </article>
        </div>

        <div class="package-rail" :aria-label="ui.allBuilds">
          <article v-for="option in downloadOptions" :key="`rail-${option.key}`" class="package-card">
            <span>{{ option.item.platform }}</span>
            <strong>{{ option.item.arch }}</strong>
          </article>
        </div>
      </section>

      <section id="docs" class="section docs-band">
        <div class="section-heading narrow">
          <h2>{{ t.sections.docsTitle }}</h2>
          <p>{{ t.sections.docsText }}</p>
        </div>

        <div class="guide-stage">
          <div class="guide-tabs" :aria-label="ui.guideSelector">
            <button
              v-for="(guide, index) in guideCards"
              :key="guide.title"
              class="guide-tab"
              :class="{ active: activeGuideIndex === index }"
              type="button"
              :aria-pressed="activeGuideIndex === index"
              @click="activeGuideIndex = index"
            >
              <span>{{ guide.step }}</span>
              <strong>{{ guide.title }}</strong>
            </button>
          </div>

          <article class="guide-card">
            <span>{{ activeGuide.badge }}</span>
            <h3>{{ activeGuide.title }}</h3>
            <p>{{ activeGuide.text }}</p>
            <div class="command-line">
              <small>{{ ui.commandLabel }}</small>
              <code>{{ activeGuide.command }}</code>
            </div>
            <a class="button secondary" :href="activeGuide.link">{{ t.labels.viewGuide }}</a>
          </article>
        </div>
      </section>

      <section id="pricing" class="section commerce-section">
        <div class="section-heading">
          <h2>{{ t.sections.pricingTitle }}</h2>
          <p>{{ t.sections.pricingText }}</p>
        </div>

        <div class="commerce-stage">
          <div class="product-selector" :aria-label="ui.productSelector">
            <button
              v-for="item in productDeck"
              :key="item.product.id"
              class="product-selector-button"
              :class="{ active: activeProductId === item.product.id }"
              type="button"
              :aria-pressed="activeProductId === item.product.id"
              @click="activeProductId = item.product.id"
            >
              <span>{{ String(item.index).padStart(2, '0') }}</span>
              <strong>{{ item.copy.name }}</strong>
              <small>{{ item.price }}</small>
            </button>
          </div>

          <article class="product-feature">
            <span>{{ activeProduct.price }}</span>
            <h3>{{ activeProduct.copy.name }}</h3>
            <p class="audience">{{ activeProduct.copy.audience }}</p>
            <div class="commerce-visual">
              <div>
                <span>{{ ui.dashboardLabel }}</span>
                <strong>{{ activeProduct.copy.name }}</strong>
              </div>
              <div class="commerce-bars" aria-hidden="true">
                <i></i>
                <i></i>
                <i></i>
              </div>
            </div>
            <div class="feature-grid">
              <div>
                <small>{{ ui.productDelivery }}</small>
                <p>{{ activeProduct.copy.delivery }}</p>
              </div>
              <div>
                <small>{{ ui.productLimits }}</small>
                <p>{{ activeProduct.copy.limits }}</p>
              </div>
            </div>
            <a class="button primary" :href="activeProduct.product.checkoutUrl">{{ productAction(activeProduct.product) }}</a>
          </article>
        </div>
      </section>

      <section id="support" class="section support-section">
        <div>
          <span class="section-label">{{ ui.supportCta }}</span>
          <h2>{{ t.sections.supportTitle }}</h2>
          <p>{{ t.sections.supportText }}</p>
          <div class="support-actions">
            <a class="button secondary" :href="siteConfig.support.ticketUrl">{{ t.labels.submitTicket }}</a>
            <a class="button quiet" :href="`mailto:${siteConfig.support.email}`">{{ t.labels.emailSupport }}</a>
          </div>
        </div>
        <aside class="validation-board" :aria-label="ui.validationLabel">
          <span>{{ ui.validationLabel }}</span>
          <ul class="metric-list">
            <li v-for="metric in validationMetrics" :key="metric">{{ metric }}</li>
          </ul>
        </aside>
      </section>

      <section id="partners" class="section partner-strip">
        <span>{{ ui.partnerCta }}</span>
        <div>
          <h2>{{ t.sections.partnersTitle }}</h2>
          <p>{{ t.sections.partnersText }}</p>
        </div>
        <a class="button primary" :href="siteConfig.support.communityUrl">{{ t.labels.registerPartner }}</a>
      </section>
    </main>
  </div>
</template>
