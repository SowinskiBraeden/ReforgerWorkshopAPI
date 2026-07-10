/*
 * Config generator page. The form is a projection over the shared working
 * config: every edit loads the latest config from storage, applies one
 * change, and saves. Unknown fields from imported configs are untouched.
 */
(function () {
  'use strict';

  var RM = window.RM;
  var formEl = document.getElementById('cg-form');
  var modsEl = document.getElementById('cg-mods');
  var previewEl = document.getElementById('cg-preview');
  var validationEl = document.getElementById('cg-validation');
  var jsonEditPanel = document.getElementById('cg-json-edit');
  var jsonEditInput = document.getElementById('cg-json-edit-text');
  var jsonEditError = document.getElementById('cg-json-edit-error');
  if (!formEl) return;
  var jsonEditEditor = jsonEditInput ? RM.mountCodeEditor(jsonEditInput) : null;

  // Base-game scenario IDs from the official server documentation.
  var SCENARIO_SUGGESTIONS = [
    { id: '{ECC61978EDCC2B5A}Missions/23_Campaign.conf', label: 'Conflict - Everon' },
    { id: '{C700DB41F0C546E1}Missions/23_Campaign_Arland.conf', label: 'Conflict - Arland' },
    { id: '{59AD59368755F41A}Missions/21_GM_Eden.conf', label: 'Game Master - Everon' },
    { id: '{94FDA7451242150B}Missions/21_GM_Arland.conf', label: 'Game Master - Arland' }
  ];

  var SECTIONS = [
    {
      legend: 'Network',
      fields: [
        { path: 'bindAddress', label: 'Bind address', type: 'text', help: '0.0.0.0 listens on all interfaces.' },
        { path: 'bindPort', label: 'Bind port (UDP)', type: 'number', help: 'Conventional game port is 2001.' },
        { path: 'publicAddress', label: 'Public address', type: 'text', help: 'Externally reachable IP. Leave empty for automatic detection.' },
        { path: 'publicPort', label: 'Public port', type: 'number' }
      ]
    },
    {
      legend: 'A2S server queries',
      fields: [
        { path: 'a2s.address', label: 'A2S address', type: 'text' },
        { path: 'a2s.port', label: 'A2S port', type: 'number', help: 'Commonly 17777. Must differ from the game port.' }
      ]
    },
    {
      legend: 'RCON (optional)',
      toggle: { path: 'rcon', label: 'Enable RCON', template: { address: '0.0.0.0', port: 19999, password: '', permission: 'admin' } },
      fields: [
        { path: 'rcon.address', label: 'RCON address', type: 'text' },
        { path: 'rcon.port', label: 'RCON port', type: 'number' },
        { path: 'rcon.password', label: 'RCON password', type: 'text', help: 'Required by the server; no spaces.' },
        { path: 'rcon.permission', label: 'Permission', type: 'select', options: ['admin', 'monitor'] }
      ]
    },
    {
      legend: 'Game',
      fields: [
        { path: 'game.name', label: 'Server name', type: 'text' },
        { path: 'game.password', label: 'Join password', type: 'text', help: 'Leave empty for a public server.' },
        { path: 'game.passwordAdmin', label: 'Admin password', type: 'text', help: 'For in-game admin login.' },
        { path: 'game.scenarioId', label: 'Scenario ID', type: 'scenario', help: 'Format {GUID}Path/File.conf. Modded scenario IDs are shown on mod pages in the mod browser.' },
        { path: 'game.maxPlayers', label: 'Max players', type: 'number', help: '1 to 128.' },
        { path: 'game.visible', label: 'Visible in server browser', type: 'bool' },
        { path: 'game.crossPlatform', label: 'Cross-platform play', type: 'bool' },
        { path: 'game.supportedPlatforms', label: 'Supported platforms', type: 'platforms' },
        { path: 'game.modsRequiredByDefault', label: 'Mods required by default', type: 'bool', help: 'Whether joining clients must run listed mods unless an entry overrides it.' }
      ]
    },
    {
      legend: 'Game properties',
      fields: [
        { path: 'game.gameProperties.enableAI', label: 'Enable AI', type: 'bool', help: 'Allows AI systems for scenarios that use AI.' },
        { path: 'game.gameProperties.serverMaxViewDistance', label: 'Server max view distance', type: 'number', help: '500 to 10000.' },
        { path: 'game.gameProperties.networkViewDistance', label: 'Network view distance', type: 'number', help: '500 to 5000.' },
        { path: 'game.gameProperties.serverMinGrassDistance', label: 'Min grass distance', type: 'number', help: '0, or 50 to 150.' },
        { path: 'game.gameProperties.disableThirdPerson', label: 'Force first person', type: 'bool' },
        { path: 'game.gameProperties.fastValidation', label: 'Fast validation', type: 'bool' },
        { path: 'game.gameProperties.battlEye', label: 'BattlEye', type: 'bool' },
        { path: 'game.gameProperties.VONDisableUI', label: 'Disable VON UI', type: 'bool', help: 'Hides the general voice-over-network UI when enabled.' },
        { path: 'game.gameProperties.VONDisableDirectSpeechUI', label: 'Disable direct speech UI', type: 'bool', help: 'Hides the direct speech voice UI when enabled.' },
        { path: 'game.gameProperties.VONCanTransmitCrossFaction', label: 'Allow cross-faction VON', type: 'bool', help: 'Allows voice transmission across factions when enabled.' },
        { path: 'game.gameProperties.missionHeader', label: 'Mission header overrides', type: 'json-object', help: 'Scenario-specific missionHeader values as a JSON object. Leave {} unless the scenario documents overrides.' }
      ]
    },
    {
      legend: 'Operating',
      fields: [
        { path: 'operating.enableAI', label: 'Enable operating AI', type: 'bool', help: 'Controls AI processing at the operating layer.' },
        { path: 'operating.lobbyPlayerSynchronise', label: 'Synchronize players in lobby', type: 'bool', help: 'Controls whether clients are synchronized while waiting in the lobby.' },
        { path: 'operating.playerSaveTime', label: 'Player save interval', type: 'number', help: 'How often player state is saved, in seconds.' },
        { path: 'operating.aiLimit', label: 'AI limit', type: 'number', help: 'Caps the number of AI entities the server may run.' },
        { path: 'operating.joinQueue.maxSize', label: 'Join queue size', type: 'number', help: 'Maximum players waiting in the join queue.' }
      ]
    }
  ];

  var PLATFORMS = [
    { value: 'PLATFORM_PC', label: 'PC' },
    { value: 'PLATFORM_XBL', label: 'Xbox' },
    { value: 'PLATFORM_PSN', label: 'PlayStation' }
  ];

  function getPath(obj, path) {
    var parts = path.split('.');
    var node = obj;
    for (var i = 0; i < parts.length; i++) {
      if (node === null || typeof node !== 'object') return undefined;
      node = node[parts[i]];
    }
    return node;
  }

  function setPath(obj, path, value) {
    var parts = path.split('.');
    var node = obj;
    for (var i = 0; i < parts.length - 1; i++) {
      if (node[parts[i]] === null || typeof node[parts[i]] !== 'object' || Array.isArray(node[parts[i]])) {
        node[parts[i]] = {};
      }
      node = node[parts[i]];
    }
    var last = parts[parts.length - 1];
    if (value === undefined) {
      delete node[last];
    } else {
      node[last] = value;
    }
  }

  function fieldId(path) {
    return 'cg-f-' + path.replace(/\./g, '-');
  }

  function renderForm() {
    var cfg = RM.ensureConfig();
    var html = SECTIONS.map(function (section) {
      var enabled = true;
      var toggleHtml = '';
      if (section.toggle) {
        enabled = getPath(cfg, section.toggle.path) !== undefined;
        toggleHtml = '<div class="form-check form-switch mb-2">' +
          '<input class="form-check-input" type="checkbox" id="' + fieldId(section.toggle.path) + '-toggle" data-toggle-path="' + section.toggle.path + '"' + (enabled ? ' checked' : '') + '>' +
          '<label class="form-check-label" for="' + fieldId(section.toggle.path) + '-toggle">' + RM.esc(section.toggle.label) + '</label></div>';
      }
      var fieldsHtml = !enabled ? '' : section.fields.map(function (field) {
        return fieldHTML(field, getPath(cfg, field.path));
      }).join('');
      return '<fieldset><legend>' + RM.esc(section.legend) + '</legend>' + toggleHtml + fieldsHtml + '</fieldset>';
    }).join('');
    formEl.innerHTML = html;
    bindForm();
  }

  function fieldHTML(field, value) {
    var id = fieldId(field.path);
    var help = field.help ? '<div class="cg-field-help">' + RM.esc(field.help) + '</div>' : '';
    if (field.type === 'bool') {
      return '<div class="form-check form-switch mb-2">' +
        '<input class="form-check-input" type="checkbox" id="' + id + '" data-path="' + field.path + '" data-type="bool"' + (value === true ? ' checked' : '') + '>' +
        '<label class="form-check-label" for="' + id + '">' + RM.esc(field.label) + '</label>' + help + '</div>';
    }
    if (field.type === 'select') {
      return '<div class="mb-2"><label class="form-label" for="' + id + '">' + RM.esc(field.label) + '</label>' +
        '<select class="form-select" id="' + id + '" data-path="' + field.path + '" data-type="select">' +
        field.options.map(function (opt) {
          return '<option value="' + RM.esc(opt) + '"' + (value === opt ? ' selected' : '') + '>' + RM.esc(opt) + '</option>';
        }).join('') + '</select>' + help + '</div>';
    }
    if (field.type === 'platforms') {
      var selected = Array.isArray(value) ? value : [];
      return '<div class="mb-2"><span class="form-label d-block">' + RM.esc(field.label) + '</span>' +
        PLATFORMS.map(function (platform) {
          var pid = id + '-' + platform.value;
          return '<div class="form-check form-check-inline">' +
            '<input class="form-check-input" type="checkbox" id="' + pid + '" data-platforms-path="' + field.path + '" value="' + platform.value + '"' +
            (selected.indexOf(platform.value) !== -1 ? ' checked' : '') + '>' +
            '<label class="form-check-label" for="' + pid + '">' + RM.esc(platform.label) + '</label></div>';
        }).join('') + help + '</div>';
    }
    if (field.type === 'scenario') {
      return '<div class="mb-2"><label class="form-label" for="' + id + '">' + RM.esc(field.label) + '</label>' +
        '<input class="form-control" type="text" id="' + id + '" data-path="' + field.path + '" data-type="text" list="cg-scenarios" value="' + RM.esc(value === undefined ? '' : value) + '">' +
        '<datalist id="cg-scenarios">' + SCENARIO_SUGGESTIONS.map(function (s) {
          return '<option value="' + RM.esc(s.id) + '">' + RM.esc(s.label) + '</option>';
        }).join('') + '</datalist>' + help + '</div>';
    }
    if (field.type === 'json-object') {
      var objectValue = (value && typeof value === 'object' && !Array.isArray(value)) ? value : {};
      return '<div class="mb-2"><label class="form-label" for="' + id + '">' + RM.esc(field.label) + '</label>' +
        '<textarea class="form-control tool-code-input" rows="4" id="' + id + '" data-path="' + field.path + '" data-type="json-object" spellcheck="false">' +
        RM.esc(JSON.stringify(objectValue, null, 2)) + '</textarea>' + help + '</div>';
    }
    var inputType = field.type === 'number' ? 'number' : 'text';
    return '<div class="mb-2"><label class="form-label" for="' + id + '">' + RM.esc(field.label) + '</label>' +
      '<input class="form-control" type="' + inputType + '" id="' + id + '" data-path="' + field.path + '" data-type="' + field.type + '" value="' + RM.esc(value === undefined ? '' : value) + '">' + help + '</div>';
  }

  function bindForm() {
    formEl.querySelectorAll('[data-path]').forEach(function (el) {
      var handler = function () {
        var cfg = RM.ensureConfig();
        var type = el.getAttribute('data-type');
        var value;
        if (type === 'bool') {
          value = el.checked;
        } else if (type === 'number') {
          value = el.value.trim() === '' ? undefined : Number(el.value);
          if (value !== undefined && !isFinite(value)) return;
        } else if (type === 'json-object') {
          var parsed = RM.parseJSONWithPos(el.value);
          if (parsed.error || parsed.value === null || typeof parsed.value !== 'object' || Array.isArray(parsed.value)) return;
          value = parsed.value;
        } else {
          value = el.value;
        }
        setPath(cfg, el.getAttribute('data-path'), value);
        RM.saveConfig(cfg);
      };
      el.addEventListener(el.tagName === 'SELECT' || el.type === 'checkbox' ? 'change' : 'input', handler);
    });

    formEl.querySelectorAll('[data-platforms-path]').forEach(function (el) {
      el.addEventListener('change', function () {
        var cfg = RM.ensureConfig();
        var path = el.getAttribute('data-platforms-path');
        var selected = [];
        formEl.querySelectorAll('[data-platforms-path="' + path + '"]').forEach(function (box) {
          if (box.checked) selected.push(box.value);
        });
        setPath(cfg, path, selected);
        RM.saveConfig(cfg);
      });
    });

    formEl.querySelectorAll('[data-toggle-path]').forEach(function (el) {
      el.addEventListener('change', function () {
        var cfg = RM.ensureConfig();
        var path = el.getAttribute('data-toggle-path');
        var section = SECTIONS.find(function (s) { return s.toggle && s.toggle.path === path; });
        if (el.checked) {
          if (getPath(cfg, path) === undefined) setPath(cfg, path, JSON.parse(JSON.stringify(section.toggle.template)));
        } else {
          setPath(cfg, path, undefined);
        }
        RM.saveConfig(cfg);
        renderForm();
      });
    });
  }

  function renderPreview() {
    var cfg = RM.ensureConfig();
    var text = RM.formatJSON(cfg);
    previewEl.classList.add('language-json');
    if (window.hljs) {
      previewEl.innerHTML = window.hljs.highlight(text, { language: 'json' }).value;
      previewEl.classList.add('hljs');
    } else {
      previewEl.textContent = text;
    }
    RM.renderFindings(validationEl, RM.validateConfig(cfg));
  }

  /* ---- Toolbar actions ---- */

  document.getElementById('cg-blank').addEventListener('click', function () {
    if (!window.confirm('Replace your current working config with a fresh blank one?')) return;
    RM.saveConfig(RM.defaultConfig());
    renderForm();
  });

  document.getElementById('cg-example').addEventListener('click', async function () {
    if (!window.confirm('Replace your current working config with the example config?')) return;
    try {
      var res = await fetch('/static/tools/example-config.json');
      RM.saveConfig(await res.json());
      renderForm();
    } catch (e) { /* leave current config in place */ }
  });

  var importPanel = document.getElementById('cg-import');
  var importText = document.getElementById('cg-import-text');
  var importError = document.getElementById('cg-import-error');
  document.getElementById('cg-import-toggle').addEventListener('click', function () {
    importPanel.classList.toggle('d-none');
    importError.innerHTML = '';
  });
  document.getElementById('cg-import-cancel').addEventListener('click', function () {
    importPanel.classList.add('d-none');
    importText.value = '';
    importError.innerHTML = '';
  });
  document.getElementById('cg-import-apply').addEventListener('click', function () {
    importConfig(importText.value);
  });
  document.getElementById('cg-file').addEventListener('change', function () {
    var file = this.files && this.files[0];
    var fileInput = this;
    if (!file) return;
    var reader = new FileReader();
    reader.onload = function () {
      importConfig(String(reader.result || ''));
      fileInput.value = '';
    };
    reader.readAsText(file);
  });

  function importConfig(text) {
    var parsed = RM.parseJSONWithPos(text);
    if (parsed.error) {
      importPanel.classList.remove('d-none');
      importText.value = text;
      importError.className = 'tool-status tool-status-error';
      importError.textContent = 'Invalid JSON' + (parsed.error.line ? ' (line ' + parsed.error.line + ')' : '') + ': ' + parsed.error.message;
      return;
    }
    if (parsed.value === null || typeof parsed.value !== 'object' || Array.isArray(parsed.value)) {
      importError.className = 'tool-status tool-status-error';
      importError.textContent = 'The config must be a JSON object.';
      return;
    }
    RM.saveConfig(parsed.value);
    importPanel.classList.add('d-none');
    importText.value = '';
    importError.innerHTML = '';
    renderForm();
  }

  document.getElementById('cg-copy').addEventListener('click', function () {
    RM.copyText(RM.formatJSON(RM.ensureConfig()), this);
  });
  document.getElementById('cg-download').addEventListener('click', function () {
    RM.downloadFile('config.json', RM.formatJSON(RM.ensureConfig()));
  });
  document.getElementById('cg-copy-mods').addEventListener('click', function () {
    RM.copyText(JSON.stringify(RM.configMods(RM.ensureConfig()), null, 2), this);
  });

  document.getElementById('cg-json-edit-toggle').addEventListener('click', function () {
    var btn = this;
    var opening = jsonEditPanel.classList.contains('d-none');
    if (opening) {
      jsonEditInput.value = RM.formatJSON(RM.ensureConfig());
      jsonEditError.innerHTML = '';
      jsonEditPanel.classList.remove('d-none');
      btn.innerHTML = '<i class="bi bi-x-lg"></i> Stop editing';
      jsonEditEditor.refresh();
      jsonEditInput.focus();
    } else {
      jsonEditPanel.classList.add('d-none');
      btn.innerHTML = '<i class="bi bi-code-slash"></i> Edit JSON directly';
      jsonEditError.innerHTML = '';
    }
  });

  document.getElementById('cg-json-edit-format').addEventListener('click', function () {
    var parsed = RM.parseJSONWithPos(jsonEditInput.value);
    if (parsed.error) {
      renderJSONEditError(parsed.error);
      return;
    }
    jsonEditInput.value = RM.formatJSON(parsed.value);
    jsonEditEditor.refresh();
    jsonEditError.innerHTML = '';
  });

  document.getElementById('cg-json-edit-cancel').addEventListener('click', function () {
    jsonEditPanel.classList.add('d-none');
    document.getElementById('cg-json-edit-toggle').innerHTML = '<i class="bi bi-code-slash"></i> Edit JSON directly';
    jsonEditError.innerHTML = '';
  });

  document.getElementById('cg-json-edit-apply').addEventListener('click', function () {
    var parsed = RM.parseJSONWithPos(jsonEditInput.value);
    if (parsed.error) {
      renderJSONEditError(parsed.error);
      return;
    }
    if (parsed.value === null || typeof parsed.value !== 'object' || Array.isArray(parsed.value)) {
      jsonEditError.className = 'tool-status tool-status-error';
      jsonEditError.textContent = 'The config must be a JSON object.';
      return;
    }
    RM.saveConfig(parsed.value);
    jsonEditPanel.classList.add('d-none');
    document.getElementById('cg-json-edit-toggle').innerHTML = '<i class="bi bi-code-slash"></i> Edit JSON directly';
    jsonEditError.innerHTML = '';
    renderForm();
  });

  function renderJSONEditError(err) {
    jsonEditError.className = 'tool-status tool-status-error';
    jsonEditError.textContent = 'Invalid JSON' + (err.line ? ' (line ' + err.line + (err.col ? ', column ' + err.col : '') + ')' : '') + ': ' + err.message;
  }

  /* ---- Wire up ---- */

  RM.mountModSearch(document.getElementById('cg-mod-search'));
  RM.mountModList(modsEl, { onChange: renderPreview });

  // Preview follows every config change; the form only rebuilds when the
  // change came from outside this page (other tab) to avoid losing focus.
  window.addEventListener('rm-config-changed', renderPreview);
  window.addEventListener('storage', function () {
    renderForm();
    renderPreview();
  });

  renderForm();
  renderPreview();
})();
