/**
 * config.js – Step 1: connection configuration forms
 */

const LS_CONFIG_KEY = 'df_config';
const LS_SAMEDB_KEY = 'df_same_db';

// ── Form builder ──────────────────────────────────────────────
function dbFormHTML(id, { showFull = true, schemaOnly = false } = {}) {
  if (schemaOnly) {
    return `
      <div class="mb-2">
        <label class="form-label">Schema 名称</label>
        <input type="text" class="form-control form-control-sm" id="${id}_schema" placeholder="public" />
      </div>`;
  }
  const full = showFull ? `
      <div class="row g-2 mb-2">
        <div class="col-8">
          <label class="form-label">Host</label>
          <input type="text" class="form-control form-control-sm" id="${id}_host" placeholder="localhost" />
        </div>
        <div class="col-4">
          <label class="form-label">Port</label>
          <input type="number" class="form-control form-control-sm" id="${id}_port" value="5432" />
        </div>
      </div>
      <div class="mb-2">
        <label class="form-label">数据库名</label>
        <input type="text" class="form-control form-control-sm" id="${id}_dbname" placeholder="postgres" />
      </div>
      <div class="row g-2 mb-2">
        <div class="col-6">
          <label class="form-label">用户名</label>
          <input type="text" class="form-control form-control-sm" id="${id}_user" placeholder="postgres" />
        </div>
        <div class="col-6">
          <label class="form-label">密码</label>
          <input type="password" class="form-control form-control-sm" id="${id}_password" />
        </div>
      </div>` : '';
  return full + `
      <div class="mb-2">
        <label class="form-label">Schema</label>
        <input type="text" class="form-control form-control-sm" id="${id}_schema" placeholder="public" />
      </div>`;
}

// ── Read form values ───────────────────────────────────────────
function readDBConfig(id, schemaOnly = false) {
  const val = k => (document.getElementById(`${id}_${k}`) || {}).value || '';
  if (schemaOnly) {
    return { schema: val('schema') || 'public' };
  }
  return {
    host:     val('host'),
    port:     parseInt(val('port'), 10) || 5432,
    dbname:   val('dbname'),
    user:     val('user'),
    password: val('password'),
    schema:   val('schema') || 'public',
  };
}

// ── Populate forms ─────────────────────────────────────────────
function populateDB(id, cfg) {
  if (!cfg) return;
  const set = (k, v) => { const el = document.getElementById(`${id}_${k}`); if (el) el.value = v || ''; };
  set('host', cfg.host); set('port', cfg.port || 5432); set('dbname', cfg.dbname);
  set('user', cfg.user); set('password', cfg.password); set('schema', cfg.schema);
}

// ── Render forms ───────────────────────────────────────────────
function renderForms(sameDB) {
  const isFirstRender = !document.getElementById('srcMain_host');

  if (isFirstRender) {
    // Build stable forms only once — they never change regardless of sameDB
    document.getElementById('srcMainForm').innerHTML = dbFormHTML('srcMain');
    document.getElementById('srcTSForm').innerHTML   = dbFormHTML('srcTS', { showFull: false, schemaOnly: true });
    document.getElementById('dstTSForm').innerHTML   = dbFormHTML('dstTS', { showFull: false, schemaOnly: true });
  }

  // dstMainForm changes when sameDB toggles
  document.getElementById('dstMainForm').innerHTML = sameDB
    ? dbFormHTML('dstMain', { showFull: false, schemaOnly: true })
    : dbFormHTML('dstMain');

  // Restore values from AppState (already snapshotted before this call on toggle,
  // or loaded from server/localStorage on initial render)
  const c = window.AppState.config;
  if (c) {
    if (isFirstRender) {
      populateDB('srcMain', c.src_main);
      const srcTSEl = document.getElementById('srcTS_schema');
      if (srcTSEl) srcTSEl.value = (c.src_ts && c.src_ts.schema) || '';
      const dstTSEl = document.getElementById('dstTS_schema');
      if (dstTSEl) dstTSEl.value = (c.dst_ts && c.dst_ts.schema) || '';
    }
    if (!sameDB) {
      populateDB('dstMain', c.dst_main);
    } else {
      const el = document.getElementById('dstMain_schema');
      if (el) el.value = (c.dst_main && c.dst_main.schema) || 'public';
    }
  }

  attachAutoSave();
}

// ── Build config object from forms ────────────────────────────
function buildConfig() {
  const sameDB  = document.getElementById('sameDbSwitch').checked;
  const srcTS   = readDBConfig('srcTS', true);
  const dstTS   = readDBConfig('dstTS', true); // always schema-only
  const srcMain = readDBConfig('srcMain');

  let dstMain;
  if (sameDB) {
    dstMain = { ...srcMain, schema: readDBConfig('dstMain', true).schema };
  } else {
    dstMain = readDBConfig('dstMain');
  }

  return {
    src_main: srcMain,
    src_ts:   srcTS,
    dst_main: dstMain,
    dst_ts:   dstTS,
    same_db:  sameDB,
  };
}

// ── Auto-save form to localStorage ────────────────────────────
function saveConfigToLocal() {
  try {
    const cfg = buildConfig();
    localStorage.setItem(LS_CONFIG_KEY, JSON.stringify(cfg));
    localStorage.setItem(LS_SAMEDB_KEY, document.getElementById('sameDbSwitch').checked ? '1' : '0');
  } catch (_) {}
}

function attachAutoSave() {
  document.querySelectorAll('#step1 input').forEach(el => {
    el.addEventListener('input', saveConfigToLocal);
    el.addEventListener('change', saveConfigToLocal);
  });
}

// ── Test connection results ────────────────────────────────────
function renderTestResult(data) {
  const items = [
    ['src_main', '源主库'],
    ['src_ts',   '源时序库'],
    ['dst_main', '目标主库'],
    ['dst_ts',   '目标时序库'],
  ];
  let html = '<div class="d-flex flex-wrap gap-2 mt-2">';
  for (const [key, label] of items) {
    if (!data[key]) continue;
    const ok = data[key].ok;
    const err = data[key].error || '';
    html += `<span class="badge ${ok ? 'bg-success' : 'bg-danger'}" title="${err}">
      ${ok ? '✓' : '✗'} ${label}${err ? ' — ' + err : ''}
    </span>`;
  }
  if (data.same_db) {
    html += `<span class="badge bg-info text-dark">同库模式</span>`;
  }
  html += '</div>';
  document.getElementById('connTestResult').innerHTML = html;
}

// ── Init ───────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', async () => {
  // Try to load saved config: first from server, then from localStorage.
  let savedCfg = null;

  try {
    const resp = await apiGet('/api/config/load');
    if (resp.ok && resp.config) {
      savedCfg = resp.config;
    }
  } catch (_) {}

  if (!savedCfg) {
    try {
      const raw = localStorage.getItem(LS_CONFIG_KEY);
      if (raw) savedCfg = JSON.parse(raw);
    } catch (_) {}
  }

  if (savedCfg) {
    window.AppState.config = savedCfg;
  }

  const savedSameDB = savedCfg ? !!savedCfg.same_db : (localStorage.getItem(LS_SAMEDB_KEY) === '1');
  const sameSwitch = document.getElementById('sameDbSwitch');
  sameSwitch.checked = savedSameDB;

  renderForms(savedSameDB);

  // If we have a saved config, unlock steps and restore last step
  if (savedCfg) {
    unlockStep(2); unlockStep(3); unlockStep(4); unlockStep(5);
    const lastStep = parseInt(localStorage.getItem('df_step') || '1', 10);
    if (lastStep > 1) {
      goToStep(lastStep);
      if (lastStep >= 2) loadTenants();
    }
  }

  // Same-DB toggle — snapshot current values first so they survive the re-render
  sameSwitch.addEventListener('change', function () {
    const prevSameDB = !this.checked; // the layout BEFORE this toggle
    const snap = {
      src_main: readDBConfig('srcMain'),
      src_ts:   readDBConfig('srcTS', true),
      // read dstMain in its CURRENT layout (full or schema-only)
      dst_main: readDBConfig('dstMain', prevSameDB),
      dst_ts:   readDBConfig('dstTS', true),
      same_db:  this.checked,
    };
    window.AppState.config = snap;
    try { localStorage.setItem(LS_CONFIG_KEY, JSON.stringify(snap)); } catch (_) {}
    renderForms(this.checked);
  });

  // Test connection
  document.getElementById('btnTestConn').addEventListener('click', async () => {
    const btn = document.getElementById('btnTestConn');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>测试中…';
    clearAlert('connTestResult');
    try {
      const cfg = buildConfig();
      const data = await apiPost('/api/config/test-connection', cfg);
      if (data.error) {
        showAlert('connTestResult', 'danger', data.error);
      } else {
        renderTestResult(data);
      }
    } catch (e) {
      showAlert('connTestResult', 'danger', '请求失败: ' + e.message);
    } finally {
      btn.disabled = false;
      btn.innerHTML = '<i class="bi bi-wifi me-1"></i>测试连接';
    }
  });

  // Save & continue
  document.getElementById('btnSaveConfig').addEventListener('click', async () => {
    const btn = document.getElementById('btnSaveConfig');
    btn.disabled = true;
    try {
      const cfg = buildConfig();
      const data = await apiPost('/api/config/save', cfg);
      if (data.error) {
        showAlert('connTestResult', 'danger', data.error);
        return;
      }
      cfg.same_db = data.same_db;
      window.AppState.config = cfg;
      localStorage.setItem(LS_CONFIG_KEY, JSON.stringify(cfg));
      unlockStep(2); unlockStep(3); unlockStep(4); unlockStep(5);
      goToStep(2);
      loadTenants();
    } catch (e) {
      showAlert('connTestResult', 'danger', '保存失败: ' + e.message);
    } finally {
      btn.disabled = false;
    }
  });
});

