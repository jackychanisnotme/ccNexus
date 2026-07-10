function nonNegativeCount(value) {
    const count = Number(value);
    if (!Number.isFinite(count) || count <= 0) {
        return 0;
    }
    return count;
}

// Stored `requests` count successful requests, while `errors` count failed attempts.
export function summarizeRequestStats(requests, errors) {
    const success = nonNegativeCount(requests);
    const failed = nonNegativeCount(errors);

    return {
        total: success + failed,
        success,
        failed
    };
}
