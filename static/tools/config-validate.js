/*
 * Client-side validation for Arma Reforger server config.json files.
 *
 * Checks are deliberately conservative: they cover JSON syntax, structure,
 * documented value ranges, and mods-array mistakes. Anything not publicly
 * documented is left alone rather than guessed at.
 *
 * TODO: if an official machine-readable config schema is ever published by
 * Bohemia Interactive, validate against it here instead of these hand-rolled
 * checks.
 */
(function () {
  'use strict';

  var PLATFORMS = ['PLATFORM_PC', 'PLATFORM_XBL', 'PLATFORM_PSN'];
  var KNOWN_ROOT_KEYS = ['bindAddress', 'bindPort', 'publicAddress', 'publicPort', 'a2s', 'rcon', 'game', 'operating'];
  var SCENARIO_RE = /^\{[0-9A-F]{16}\}.+/i;

  /* Parse JSON and, on failure, derive a line/column from the error. */
  function parseJSONWithPos(text) {
    try {
      return { value: JSON.parse(text) };
    } catch (err) {
      var message = String(err.message || 'Invalid JSON');
      var line = null;
      var col = null;
      var lineColMatch = message.match(/line (\d+) column (\d+)/i);
      var posMatch = message.match(/position (\d+)/i);
      if (lineColMatch) {
        line = parseInt(lineColMatch[1], 10);
        col = parseInt(lineColMatch[2], 10);
      } else if (posMatch) {
        var pos = parseInt(posMatch[1], 10);
        var before = text.slice(0, pos);
        line = before.split('\n').length;
        col = pos - before.lastIndexOf('\n');
      }
      return { error: { message: message, line: line, col: col } };
    }
  }

  function isObject(value) {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
  }

  function isInt(value) {
    return typeof value === 'number' && isFinite(value) && Math.floor(value) === value;
  }

  /* validateConfig(parsedConfig) -> [{level, path, message}] */
  function validateConfig(cfg) {
    var findings = [];
    var add = function (level, path, message) {
      findings.push({ level: level, path: path, message: message });
    };

    if (!isObject(cfg)) {
      add('error', '', 'The config must be a JSON object, not ' + (Array.isArray(cfg) ? 'an array' : typeof cfg) + '.');
      return findings;
    }

    var checkPort = function (path, value, required) {
      if (value === undefined) {
        if (required) add('warning', path, 'No port set; the server will use its default.');
        return;
      }
      if (!isInt(value)) {
        add('error', path, 'Must be a number (no quotes), got ' + JSON.stringify(value) + '.');
        return;
      }
      if (value < 1 || value > 65535) {
        add('error', path, 'Must be a port between 1 and 65535.');
      }
    };

    var checkString = function (path, value) {
      if (value !== undefined && typeof value !== 'string') {
        add('error', path, 'Must be a string.');
      }
    };

    var checkBool = function (path, value) {
      if (value !== undefined && typeof value !== 'boolean') {
        add('error', path, 'Must be true or false (no quotes).');
      }
    };

    var checkRange = function (path, value, min, max) {
      if (value === undefined) return;
      if (!isInt(value)) {
        add('error', path, 'Must be a number, got ' + JSON.stringify(value) + '.');
        return;
      }
      if (value < min || value > max) {
        add('warning', path, 'Documented range is ' + min + ' to ' + max + '.');
      }
    };

    checkString('bindAddress', cfg.bindAddress);
    checkString('publicAddress', cfg.publicAddress);
    checkPort('bindPort', cfg.bindPort);
    checkPort('publicPort', cfg.publicPort);

    if (cfg.a2s !== undefined) {
      if (!isObject(cfg.a2s)) {
        add('error', 'a2s', 'Must be an object with address and port.');
      } else {
        checkString('a2s.address', cfg.a2s.address);
        checkPort('a2s.port', cfg.a2s.port);
      }
    }

    if (cfg.rcon !== undefined) {
      if (!isObject(cfg.rcon)) {
        add('error', 'rcon', 'Must be an object. Remove it entirely if you do not use RCON.');
      } else {
        checkString('rcon.address', cfg.rcon.address);
        checkPort('rcon.port', cfg.rcon.port);
        if (typeof cfg.rcon.password !== 'string' || cfg.rcon.password.length === 0) {
          add('warning', 'rcon.password', 'RCON needs a password; the server rejects an empty one.');
        } else if (/\s/.test(cfg.rcon.password)) {
          add('warning', 'rcon.password', 'The RCON password must not contain spaces.');
        }
        if (cfg.rcon.permission !== undefined && cfg.rcon.permission !== 'admin' && cfg.rcon.permission !== 'monitor') {
          add('warning', 'rcon.permission', 'Documented values are "admin" or "monitor".');
        }
      }
    }

    // Ports that collide make the server or its query interface unreachable.
    var portOf = function (v) { return isInt(v) ? v : null; };
    var usedPorts = [
      ['bindPort', portOf(cfg.bindPort)],
      ['a2s.port', cfg.a2s && portOf(cfg.a2s.port)],
      ['rcon.port', cfg.rcon && portOf(cfg.rcon.port)]
    ].filter(function (p) { return p[1] !== null && p[1] !== undefined; });
    for (var i = 0; i < usedPorts.length; i++) {
      for (var j = i + 1; j < usedPorts.length; j++) {
        if (usedPorts[i][1] === usedPorts[j][1]) {
          add('warning', usedPorts[j][0], 'Same port as ' + usedPorts[i][0] + ' (' + usedPorts[i][1] + '); these should differ.');
        }
      }
    }

    if (cfg.mods !== undefined) {
      add('warning', 'mods', 'A top-level mods array is ignored by the server. Move it inside the game object (game.mods).');
    }

    if (!isObject(cfg.game)) {
      add('error', 'game', cfg.game === undefined
        ? 'Missing the required game object (server name, scenarioId, mods, ...).'
        : 'Must be an object.');
      return findings;
    }

    var game = cfg.game;
    checkString('game.name', game.name);
    if (game.name === '') add('warning', 'game.name', 'Empty server name; players will see a blank entry.');
    checkString('game.password', game.password);
    checkString('game.passwordAdmin', game.passwordAdmin);
    checkBool('game.visible', game.visible);
    checkBool('game.crossPlatform', game.crossPlatform);
    checkBool('game.modsRequiredByDefault', game.modsRequiredByDefault);

    if (game.admins !== undefined && !Array.isArray(game.admins)) {
      add('error', 'game.admins', 'Must be an array of player identifiers.');
    }

    if (typeof game.scenarioId !== 'string' || game.scenarioId.trim() === '') {
      add('error', 'game.scenarioId', 'Required. The server needs a scenario, e.g. {ECC61978EDCC2B5A}Missions/23_Campaign.conf.');
    } else if (!SCENARIO_RE.test(game.scenarioId.trim())) {
      add('warning', 'game.scenarioId', 'Unusual format; expected {GUID}Path/File.conf.');
    }

    if (game.maxPlayers !== undefined) {
      if (!isInt(game.maxPlayers)) {
        add('error', 'game.maxPlayers', 'Must be a number.');
      } else if (game.maxPlayers < 1 || game.maxPlayers > 128) {
        add('warning', 'game.maxPlayers', 'Documented range is 1 to 128.');
      }
    }

    if (game.supportedPlatforms !== undefined) {
      if (!Array.isArray(game.supportedPlatforms)) {
        add('error', 'game.supportedPlatforms', 'Must be an array of platform identifiers.');
      } else {
        game.supportedPlatforms.forEach(function (p, idx) {
          if (typeof p !== 'string' || PLATFORMS.indexOf(p) === -1) {
            add('warning', 'game.supportedPlatforms[' + idx + ']', 'Unknown platform ' + JSON.stringify(p) + '; documented values are ' + PLATFORMS.join(', ') + '.');
          }
        });
      }
    }

    if (game.gameProperties !== undefined) {
      if (!isObject(game.gameProperties)) {
        add('error', 'game.gameProperties', 'Must be an object.');
      } else {
        var gp = game.gameProperties;
        checkBool('game.gameProperties.enableAI', gp.enableAI);
        checkRange('game.gameProperties.serverMaxViewDistance', gp.serverMaxViewDistance, 500, 10000);
        checkRange('game.gameProperties.networkViewDistance', gp.networkViewDistance, 500, 5000);
        checkRange('game.gameProperties.serverMinGrassDistance', gp.serverMinGrassDistance, 0, 150);
        checkBool('game.gameProperties.disableThirdPerson', gp.disableThirdPerson);
        checkBool('game.gameProperties.fastValidation', gp.fastValidation);
        checkBool('game.gameProperties.battlEye', gp.battlEye);
        checkBool('game.gameProperties.VONDisableUI', gp.VONDisableUI);
        checkBool('game.gameProperties.VONDisableDirectSpeechUI', gp.VONDisableDirectSpeechUI);
        checkBool('game.gameProperties.VONCanTransmitCrossFaction', gp.VONCanTransmitCrossFaction);
        if (gp.missionHeader !== undefined && !isObject(gp.missionHeader)) {
          add('error', 'game.gameProperties.missionHeader', 'Must be an object with scenario-specific overrides.');
        }
      }
    }

    if (game.mods !== undefined) {
      if (!Array.isArray(game.mods)) {
        add('error', 'game.mods', 'Must be an array of mod objects.');
      } else {
        var seen = {};
        game.mods.forEach(function (mod, idx) {
          var path = 'game.mods[' + idx + ']';
          if (!isObject(mod)) {
            add('error', path, 'Each entry must be an object like {"modId": "..."} - got ' + JSON.stringify(mod) + '.');
            return;
          }
          if (typeof mod.modId !== 'string' || mod.modId.trim() === '') {
            add('error', path + '.modId', 'Required. Each mod entry needs a Workshop mod ID string.');
          } else {
            var id = mod.modId.trim().toUpperCase();
            if (!window.RM.MOD_ID_RE.test(id)) {
              add('warning', path + '.modId', JSON.stringify(mod.modId) + ' does not look like a Workshop ID (16 hexadecimal characters).');
            }
            if (seen[id] !== undefined) {
              add('error', path + '.modId', 'Duplicate of game.mods[' + seen[id] + '] (' + id + ').');
            } else {
              seen[id] = idx;
            }
          }
          checkString(path + '.name', mod.name);
          checkString(path + '.version', mod.version);
          checkBool(path + '.required', mod.required);
        });
      }
    }

    if (cfg.operating !== undefined) {
      if (!isObject(cfg.operating)) {
        add('error', 'operating', 'Must be an object.');
      } else {
        checkBool('operating.enableAI', cfg.operating.enableAI);
        checkBool('operating.lobbyPlayerSynchronise', cfg.operating.lobbyPlayerSynchronise);
        checkRange('operating.playerSaveTime', cfg.operating.playerSaveTime, 10, 3600);
        checkRange('operating.aiLimit', cfg.operating.aiLimit, 0, 1000);
        if (cfg.operating.joinQueue !== undefined) {
          if (!isObject(cfg.operating.joinQueue)) {
            add('error', 'operating.joinQueue', 'Must be an object.');
          } else {
            checkRange('operating.joinQueue.maxSize', cfg.operating.joinQueue.maxSize, 0, 1000);
          }
        }
      }
    }

    Object.keys(cfg).forEach(function (key) {
      if (KNOWN_ROOT_KEYS.indexOf(key) === -1 && key !== 'mods') {
        add('info', key, 'Not a field this validator knows; left unchecked.');
      }
    });

    return findings;
  }

  /* Render findings into a container. Returns counts by level. */
  function renderFindings(container, findings) {
    var counts = { error: 0, warning: 0, info: 0 };
    if (!findings.length) {
      container.innerHTML = '<div class="finding finding-ok"><i class="bi bi-check-circle"></i><span>No problems found.</span></div>';
      return counts;
    }
    var icons = { error: 'bi-x-circle', warning: 'bi-exclamation-triangle', info: 'bi-info-circle' };
    container.innerHTML = findings.map(function (f) {
      counts[f.level]++;
      var where = f.path ? '<code>' + window.RM.esc(f.path) + '</code> ' : '';
      return '<div class="finding finding-' + f.level + '"><i class="bi ' + icons[f.level] + '"></i><span>' +
        where + window.RM.esc(f.message) + '</span></div>';
    }).join('');
    return counts;
  }

  window.RM.parseJSONWithPos = parseJSONWithPos;
  window.RM.validateConfig = validateConfig;
  window.RM.renderFindings = renderFindings;
})();
