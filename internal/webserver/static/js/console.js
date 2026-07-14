// Multi-tab console shell. Each tab is an <iframe> loading the existing
// terminal.html (?embed=1) so all terminal/SFTP/clipboard logic is reused
// untouched; the shell only manages tabs (add / switch / close).
(function () {
    'use strict';

    var tabsEl = document.getElementById('tabs');
    var framesEl = document.getElementById('frames');
    var emptyEl = document.getElementById('empty');
    var addBtn = document.getElementById('add-tab');
    var picker = document.getElementById('picker');
    var pickerInput = document.getElementById('picker-input');
    var pickerList = document.getElementById('picker-list');

    var tabs = [];        // { id, username, ip, device, iframe, tabEl }
    var activeId = null;
    var seq = 0;
    var creds = null;     // cached credential list for the picker
    var hiIndex = 0;      // highlighted picker row

    function targetURL(c) {
        return '/terminal.html?embed=1' +
            '&username=' + encodeURIComponent(c.username) +
            '&ip=' + encodeURIComponent(c.ip || '') +
            '&device=' + encodeURIComponent(c.device || c.ip || '');
    }

    function openTab(c) {
        var id = ++seq;

        var iframe = document.createElement('iframe');
        iframe.src = targetURL(c);
        iframe.dataset.id = String(id);
        framesEl.appendChild(iframe);

        var tabEl = document.createElement('div');
        tabEl.className = 'tab';
        tabEl.dataset.id = String(id);
        var name = c.username + '@' + (c.device || c.ip || '');
        tabEl.innerHTML = '<span class="dot"></span><span class="label"></span>' +
                          '<span class="x" title="Close">×</span>';
        tabEl.querySelector('.label').textContent = name;
        tabEl.title = name;
        tabEl.addEventListener('click', function (e) {
            if (e.target.classList.contains('x')) { closeTab(id); return; }
            activate(id);
        });
        tabsEl.appendChild(tabEl);

        tabs.push({ id: id, username: c.username, ip: c.ip, device: c.device, iframe: iframe, tabEl: tabEl, name: name });
        activate(id);
        updateEmpty();
    }

    function activate(id) {
        activeId = id;
        tabs.forEach(function (t) {
            var on = t.id === id;
            t.iframe.classList.toggle('active', on);
            t.tabEl.classList.toggle('active', on);
            if (on) {
                document.title = 'SEGURA — ' + t.name;
                // Give keyboard focus to the terminal in this tab.
                try { t.iframe.contentWindow.focus(); } catch (e) {}
            }
        });
    }

    function closeTab(id) {
        var idx = tabs.findIndex(function (t) { return t.id === id; });
        if (idx < 0) return;
        var t = tabs[idx];
        // Removing the iframe tears down its WebSocket / session.
        t.iframe.src = 'about:blank';
        t.iframe.remove();
        t.tabEl.remove();
        tabs.splice(idx, 1);

        if (activeId === id) {
            var next = tabs[idx] || tabs[idx - 1];
            if (next) activate(next.id);
            else activeId = null;
        }
        updateEmpty();
    }

    function updateEmpty() {
        emptyEl.classList.toggle('show', tabs.length === 0);
    }

    // ---- Picker ----
    function showPicker() {
        picker.classList.add('show');
        pickerInput.value = '';
        hiIndex = 0;
        loadCreds().then(function () { renderPicker(''); });
        setTimeout(function () { pickerInput.focus(); }, 30);
    }
    function hidePicker() {
        picker.classList.remove('show');
        if (activeId != null) activate(activeId);
    }

    function loadCreds() {
        if (creds) return Promise.resolve(creds);
        return fetch('/api/credentials').then(function (r) { return r.json(); }).then(function (data) {
            creds = (data && data.credentials) || data || [];
            if (!Array.isArray(creds)) creds = [];
            return creds;
        }).catch(function () { creds = []; return creds; });
    }

    function filtered(q) {
        q = q.trim().toLowerCase();
        if (!q) return creds.slice();
        return creds.filter(function (c) {
            return (c.username + ' ' + (c.device || '') + ' ' + (c.ip || '')).toLowerCase().indexOf(q) !== -1;
        });
    }

    function renderPicker(q) {
        var list = filtered(q);
        if (hiIndex >= list.length) hiIndex = Math.max(0, list.length - 1);
        pickerList.innerHTML = '';
        if (list.length === 0) {
            var e = document.createElement('div');
            e.className = 'empty';
            e.textContent = creds.length ? 'No match.' : 'No credentials available.';
            pickerList.appendChild(e);
            return;
        }
        list.forEach(function (c, i) {
            var row = document.createElement('div');
            row.className = 'row' + (i === hiIndex ? ' hi' : '');
            var u = document.createElement('span'); u.className = 'u';
            u.textContent = c.username + '@' + (c.device || c.ip || '');
            var d = document.createElement('span'); d.className = 'd'; d.textContent = c.ip || '';
            row.appendChild(u); row.appendChild(d);
            row.addEventListener('click', function () { openTab(c); hidePicker(); });
            row.addEventListener('mousemove', function () {
                if (hiIndex !== i) { hiIndex = i; markHi(); }
            });
            pickerList.appendChild(row);
        });
    }

    function markHi() {
        var rows = pickerList.querySelectorAll('.row');
        rows.forEach(function (r, i) { r.classList.toggle('hi', i === hiIndex); });
        var cur = rows[hiIndex];
        if (cur) cur.scrollIntoView({ block: 'nearest' });
    }

    pickerInput.addEventListener('input', function () { hiIndex = 0; renderPicker(pickerInput.value); });
    pickerInput.addEventListener('keydown', function (e) {
        var list = filtered(pickerInput.value);
        if (e.key === 'ArrowDown') { e.preventDefault(); hiIndex = Math.min(list.length - 1, hiIndex + 1); markHi(); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); hiIndex = Math.max(0, hiIndex - 1); markHi(); }
        else if (e.key === 'Enter') { e.preventDefault(); if (list[hiIndex]) { openTab(list[hiIndex]); hidePicker(); } }
        else if (e.key === 'Escape') { e.preventDefault(); hidePicker(); }
    });
    picker.addEventListener('mousedown', function (e) { if (e.target === picker) hidePicker(); });

    addBtn.addEventListener('click', showPicker);
    document.getElementById('empty-add').addEventListener('click', showPicker);

    // Ctrl/Cmd+T → new tab; Ctrl/Cmd+W → close active tab (best-effort; browsers
    // may reserve these, but they work when the shell has focus).
    window.addEventListener('keydown', function (e) {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 't') { e.preventDefault(); showPicker(); }
    });

    // ---- Boot: open the tab from the URL, or show the picker if none. ----
    var p = new URLSearchParams(location.search);
    var u = p.get('username');
    if (u) {
        openTab({ username: u, ip: p.get('ip') || '', device: p.get('device') || '' });
    } else {
        updateEmpty();
        showPicker();
    }
})();
