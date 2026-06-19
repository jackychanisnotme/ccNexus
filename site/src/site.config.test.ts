import { describe, expect, it } from 'vitest';
import { getCommerceActionLabel, siteConfig } from './site.config';

describe('siteConfig', () => {
  it('exposes v6.1.4 release metadata and required platform downloads', () => {
    expect(siteConfig.release.version).toBe('6.1.4');
    expect(siteConfig.downloads.map((item) => `${item.platform}-${item.arch}`)).toEqual(
      expect.arrayContaining([
        'macOS-arm64',
        'macOS-amd64',
        'Windows-amd64',
        'Linux-amd64',
        'Docker-server'
      ])
    );
  });

  it('marks placeholder commerce links as contact actions', () => {
    const product = siteConfig.commerce.products.find((item) => item.id === 'api-entry');

    expect(product).toBeDefined();
    expect(product?.checkoutUrl).toBe('#contact-sales');
    expect(getCommerceActionLabel(product)).toBe('联系获取');
    expect(siteConfig.commerce.products.map(getCommerceActionLabel)).toEqual([
      '联系获取',
      '联系获取',
      '联系获取',
      '联系获取'
    ]);
  });

  it('uses deployable public documentation links for static hosting', () => {
    expect(siteConfig.links.dockerDocs).toMatch(/^https:\/\/github\.com\//);
    expect(siteConfig.links.codexDocs).toMatch(/^https:\/\/github\.com\//);
    expect(siteConfig.links.deploymentDocs).toMatch(/^https:\/\/github\.com\//);
  });

  it('configures an IP geo endpoint for first-visit language detection', () => {
    expect(siteConfig.localization.geoEndpoint).toMatch(/^https:\/\//);
    expect(siteConfig.localization.countryCodeField).toBe('country_code');
  });
});
