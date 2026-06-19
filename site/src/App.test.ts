import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import App from './App.vue';

describe('AINexus distribution site', () => {
  it('renders the first-stage distribution pages and contact CTAs in English by default', () => {
    const wrapper = mount(App);

    expect(wrapper.text()).toContain('AINexus');
    expect(wrapper.text()).toContain('Download macOS');
    expect(wrapper.text()).toContain('Download Windows');
    expect(wrapper.text()).toContain('Config Pack');
    expect(wrapper.text()).toContain('Node Subscription');
    expect(wrapper.text()).toContain('Managed Deployment');
    expect(wrapper.text()).toContain('API Entry');
    expect(wrapper.text()).toContain('Contact');
  });

  it('lets visitors switch from IP/browser-selected English to Chinese', async () => {
    const wrapper = mount(App);

    await wrapper.get('button.language-button').trigger('click');

    expect(wrapper.text()).toContain('下载 macOS');
    expect(wrapper.text()).toContain('下载 Windows');
    expect(wrapper.text()).toContain('配置包');
    expect(wrapper.text()).toContain('节点订阅');
    expect(wrapper.text()).toContain('代部署');
    expect(wrapper.text()).toContain('API 入口');
    expect(wrapper.text()).toContain('联系获取');
  });

  it('updates interactive product modules when visitors choose another option', async () => {
    const wrapper = mount(App);

    const windowsButton = wrapper
      .findAll('button.selector-button')
      .find((button) => button.text().includes('Windows x64'));
    expect(windowsButton).toBeTruthy();
    await windowsButton!.trigger('click');

    expect(wrapper.text()).toContain('Windows x64');
    expect(wrapper.text()).toContain('Phase one ships at least an x64 portable package');
    expect(windowsButton!.attributes('aria-pressed')).toBe('true');

    const claudeGuide = wrapper.findAll('button.guide-tab').find((button) => button.text().includes('Claude Code'));
    expect(claudeGuide).toBeTruthy();
    await claudeGuide!.trigger('click');

    expect(wrapper.text()).toContain('ANTHROPIC_BASE_URL=http://127.0.0.1:3000');
    expect(claudeGuide!.attributes('aria-pressed')).toBe('true');

    const apiProduct = wrapper.findAll('button.product-selector-button').find((button) => button.text().includes('API Entry'));
    expect(apiProduct).toBeTruthy();
    await apiProduct!.trigger('click');

    expect(wrapper.text()).toContain('Dedicated base_url');
    expect(apiProduct!.attributes('aria-pressed')).toBe('true');
  });
});
