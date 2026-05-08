const STATUS_KEY = 'ccNexus_endpointTestStatus';
const AUTO_TEST_KEY = 'ccNexus_endpointAutoTestAt';
const AUTO_TEST_INTERVAL_MS = 10 * 1000;

const inFlightTests = new Map();

function readStorageMap(key) {
    try {
        return JSON.parse(localStorage.getItem(key) || '{}');
    } catch {
        return {};
    }
}

function writeStorageMap(key, value) {
    localStorage.setItem(key, JSON.stringify(value));
}

function endpointFingerprint(endpoint) {
    return [
        endpoint.name,
        endpoint.apiUrl,
        endpoint.transformer,
        endpoint.model || '',
        endpoint.enabled ? '1' : '0'
    ].join('|');
}

export function getEndpointTestStatus(endpointName) {
    return readStorageMap(STATUS_KEY)[endpointName];
}

export function saveEndpointTestStatus(endpointName, success) {
    const statusMap = readStorageMap(STATUS_KEY);
    statusMap[endpointName] = success;
    writeStorageMap(STATUS_KEY, statusMap);
}

export async function autoTestEndpoint(apiClient, endpoint, options = {}) {
    if (!endpoint || !endpoint.name || !endpoint.enabled) {
        return { skipped: true };
    }

    const force = options.force === true;
    const fingerprint = endpointFingerprint(endpoint);
    const autoTestMap = readStorageMap(AUTO_TEST_KEY);
    const lastTest = autoTestMap[endpoint.name];
    const now = Date.now();

    if (!force && lastTest && lastTest.fingerprint === fingerprint && now - lastTest.at < AUTO_TEST_INTERVAL_MS) {
        return { skipped: true };
    }

    if (inFlightTests.has(endpoint.name)) {
        return inFlightTests.get(endpoint.name);
    }

    autoTestMap[endpoint.name] = { at: now, fingerprint };
    writeStorageMap(AUTO_TEST_KEY, autoTestMap);

    const testPromise = apiClient.testEndpoint(endpoint.name)
        .then(result => {
            const success = result.success === true;
            saveEndpointTestStatus(endpoint.name, success);
            options.onUpdate?.(endpoint.name, success);
            return { success, result };
        })
        .catch(error => {
            saveEndpointTestStatus(endpoint.name, false);
            options.onUpdate?.(endpoint.name, false);
            return { success: false, error };
        })
        .finally(() => {
            inFlightTests.delete(endpoint.name);
        });

    inFlightTests.set(endpoint.name, testPromise);
    return testPromise;
}

export function autoTestEndpoints(apiClient, endpoints, options = {}) {
    const enabledEndpoints = (endpoints || []).filter(endpoint => endpoint.enabled);
    return Promise.all(enabledEndpoints.map(endpoint => autoTestEndpoint(apiClient, endpoint, options)));
}
