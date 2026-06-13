// API Client for ccNexus
class APIClient {
    constructor(baseURL = '/api') {
        this.baseURL = baseURL;
    }

    async request(method, path, data = null) {
        const options = {
            method,
            headers: {
                'Content-Type': 'application/json'
            }
        };

        if (data) {
            options.body = JSON.stringify(data);
        }

        try {
            const response = await fetch(`${this.baseURL}${path}`, options);
            const result = await response.json();

            if (!response.ok) {
                throw new Error(result.error || 'Request failed');
            }

            return result.data || result;
        } catch (error) {
            console.error(`API Error [${method} ${path}]:`, error);
            throw error;
        }
    }

    // Endpoint management
    async getEndpoints() {
        return this.request('GET', '/endpoints');
    }

    async createEndpoint(data) {
        return this.request('POST', '/endpoints', data);
    }

    async updateEndpoint(name, data) {
        return this.request('PUT', `/endpoints/${encodeURIComponent(name)}`, data);
    }

    async deleteEndpoint(name) {
        return this.request('DELETE', `/endpoints/${encodeURIComponent(name)}`);
    }

    async toggleEndpoint(name, enabled) {
        return this.request('PATCH', `/endpoints/${encodeURIComponent(name)}/toggle`, { enabled });
    }

    async testEndpoint(name) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/test`);
    }

    async reorderEndpoints(names) {
        return this.request('POST', '/endpoints/reorder', { names });
    }

    async getCurrentEndpoint() {
        return this.request('GET', '/endpoints/current');
    }

    async switchEndpoint(name) {
        return this.request('POST', '/endpoints/switch', { name });
    }

    async fetchModels(apiUrl, apiKey, transformer, proxyUrl = '') {
        return this.request('POST', '/endpoints/fetch-models', { apiUrl, apiKey, transformer, proxyUrl });
    }

    async getEndpointCredentials(name) {
        return this.request('GET', `/endpoints/${encodeURIComponent(name)}/credentials`);
    }

    async importEndpointCredentials(name, data) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/credentials/import`, data);
    }

    async updateEndpointCredential(name, id, data) {
        return this.request('PATCH', `/endpoints/${encodeURIComponent(name)}/credentials/${id}`, data);
    }

    async deleteEndpointCredential(name, id) {
        return this.request('DELETE', `/endpoints/${encodeURIComponent(name)}/credentials/${id}`);
    }

    async startCodexCredentialAuth(name) {
        return this.request('POST', `/endpoints/${encodeURIComponent(name)}/credentials/auth/start`);
    }

    async getCodexCredentialAuthStatus(name, loginId) {
        return this.request('GET', `/endpoints/${encodeURIComponent(name)}/credentials/auth/${encodeURIComponent(loginId)}`);
    }

    async cancelCodexCredentialAuth(name, loginId) {
        return this.request('DELETE', `/endpoints/${encodeURIComponent(name)}/credentials/auth/${encodeURIComponent(loginId)}`);
    }

    // Statistics
    async getStatsSummary() {
        return this.request('GET', '/stats/summary');
    }

    statsQuery(params = {}) {
        const query = new URLSearchParams();
        if (params.endpoint) query.set('endpoint', params.endpoint);
        if (params.clientIp) query.set('clientIp', params.clientIp);
        if (params.clientIpQuery && !params.clientIp) query.set('clientIpQuery', params.clientIpQuery);
        const encoded = query.toString();
        return encoded ? `?${encoded}` : '';
    }

    async getStatsDaily(params = {}) {
        return this.request('GET', `/stats/daily${this.statsQuery(params)}`);
    }

    async getStatsWeekly(params = {}) {
        return this.request('GET', `/stats/weekly${this.statsQuery(params)}`);
    }

    async getStatsMonthly(params = {}) {
        return this.request('GET', `/stats/monthly${this.statsQuery(params)}`);
    }

    async getStatsFilters() {
        return this.request('GET', '/stats/filters');
    }

    async getStatsTrends() {
        return this.request('GET', '/stats/trends');
    }

    // Configuration
    async getConfig() {
        return this.request('GET', '/config');
    }

    async updateConfig(data) {
        return this.request('PUT', '/config', data);
    }

    async getPort() {
        return this.request('GET', '/config/port');
    }

    async updatePort(port) {
        return this.request('PUT', '/config/port', { port });
    }

    async getLogLevel() {
        return this.request('GET', '/config/log-level');
    }

    async updateLogLevel(logLevel) {
        return this.request('PUT', '/config/log-level', { logLevel });
    }

    async getNetwork() {
        return this.request('GET', '/network');
    }

    async updateNetwork(listenMode, port) {
        const payload = { listenMode };
        if (port !== undefined && port !== null) {
            payload.port = port;
        }
        return this.request('PUT', '/network', payload);
    }
}

export const api = new APIClient();
