import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import { summarizeRequestStats } from '../src/utils/stats.js';

describe('statistics request summary', () => {
    it('treats stored requests as successful requests', () => {
        assert.deepEqual(summarizeRequestStats(5, 2), {
            total: 7,
            success: 5,
            failed: 2
        });
    });

    it('does not show negative successes for error-only statistics', () => {
        assert.deepEqual(summarizeRequestStats(0, 66), {
            total: 66,
            success: 0,
            failed: 66
        });
    });

    it('normalizes invalid and negative counters', () => {
        assert.deepEqual(summarizeRequestStats(-3, Number.NaN), {
            total: 0,
            success: 0,
            failed: 0
        });
    });
});
