(function() {
    'use strict';

    var credentialsEl = document.getElementById('credentials');
    var loadingEl = document.getElementById('loading');
    var errorEl = document.getElementById('error');
    var statusEl = document.getElementById('status');
    var countEl = document.getElementById('credCount');
    var filterEl = document.getElementById('credFilter');
    var allCreds = [];

    function loadCredentials(force) {
        loadingEl.style.display = 'block';
        errorEl.style.display = 'none';
        credentialsEl.innerHTML = '';
        if (countEl) countEl.textContent = force ? 'Refreshing…' : 'Loading available credentials…';

        fetch('/api/credentials' + (force ? '?refresh=1' : ''))
            .then(function(resp) {
                if (!resp.ok) throw new Error('Failed to fetch credentials (HTTP ' + resp.status + ')');
                return resp.json();
            })
            .then(function(creds) {
                loadingEl.style.display = 'none';
                statusEl.textContent = 'Connected';
                statusEl.className = 'status-badge connected';
                allCreds = creds || [];

                if (allCreds.length === 0) {
                    credentialsEl.innerHTML = '<p style="color:var(--text-muted)">No credentials available.</p>';
                    if (countEl) countEl.textContent = 'No credentials';
                    return;
                }

                allCreds.forEach(function(cred) {
                    var card = document.createElement('div');
                    card.className = 'credential-card';
                    card.dataset.search = ((cred.username || '') + ' ' + (cred.device || '') + ' ' + (cred.ip || '')).toLowerCase();
                    card.innerHTML =
                        '<div class="username">' + escapeHtml(cred.username) + '</div>' +
                        '<div class="device">' + escapeHtml(cred.device || 'Unknown device') + '</div>' +
                        '<div class="ip">' + escapeHtml(cred.ip) + '</div>' +
                        '<div class="connect-hint">Connect</div>';
                    card.onclick = function() {
                        window.location.href = '/console.html?username=' +
                            encodeURIComponent(cred.username) +
                            '&ip=' + encodeURIComponent(cred.ip) +
                            '&device=' + encodeURIComponent(cred.device || '');
                    };
                    credentialsEl.appendChild(card);
                });
                applyFilter();
            })
            .catch(function(err) {
                loadingEl.style.display = 'none';
                errorEl.style.display = 'block';
                errorEl.textContent = err.message;
                statusEl.textContent = 'Error';
                statusEl.className = 'status-badge error';
                if (countEl) countEl.textContent = '';
            });
    }

    function applyFilter() {
        var q = (filterEl && filterEl.value || '').trim().toLowerCase();
        var cards = credentialsEl.querySelectorAll('.credential-card');
        var shown = 0;
        cards.forEach(function(card) {
            var match = !q || (card.dataset.search || '').indexOf(q) !== -1;
            card.classList.toggle('filtered-out', !match);
            if (match) shown++;
        });
        if (countEl) {
            countEl.textContent = q
                ? shown + ' of ' + allCreds.length + ' credential' + (allCreds.length === 1 ? '' : 's')
                : allCreds.length + ' credential' + (allCreds.length === 1 ? '' : 's') + ' available';
        }
    }

    if (filterEl) filterEl.addEventListener('input', applyFilter);

    function escapeHtml(str) {
        var div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    // Expose for refresh button
    window.loadCredentials = loadCredentials;

    // Load on page ready
    loadCredentials();
})();
