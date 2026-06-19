import { describe, expect, it } from 'vitest';
import {
  isLocale,
  resolveLocaleFromCountry,
  resolveLocaleFromNavigator,
  type Locale
} from './i18n';

describe('site i18n locale resolution', () => {
  it.each(['CN', 'HK', 'MO', 'TW'])('uses Chinese for %s IP countries', (country) => {
    expect(resolveLocaleFromCountry(country)).toBe('zh-CN');
  });

  it.each(['US', 'GB', 'JP', 'DE', 'SG', ''])('uses English for non-Chinese IP countries: %s', (country) => {
    expect(resolveLocaleFromCountry(country)).toBe('en-US');
  });

  it('falls back to browser language when IP lookup is unavailable', () => {
    expect(resolveLocaleFromNavigator(['zh-TW', 'en-US'])).toBe('zh-CN');
    expect(resolveLocaleFromNavigator(['en-US', 'zh-CN'])).toBe('en-US');
  });

  it('validates supported locales', () => {
    expect(isLocale('zh-CN')).toBe(true);
    expect(isLocale('en-US')).toBe(true);
    expect(isLocale('fr-FR' as Locale)).toBe(false);
  });
});
