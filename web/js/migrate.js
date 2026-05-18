/**
 * migrate.js – Step 3/4: migration options and progress via WebSocket
 */

const STAGE_LABELS = {
  schema:     '表结构',
  data:       '数据',
  timeseries: '时序',
  fk:         '外键',
  index:      '索引',
  done:       '完成',
  error:      '错误',
};

let wsConn = null;

function setBadge(id, state) {
  const el = document.getElementById(id);
  if (!el) return;
  el.className = 'badge badge-' + state;
  // set text
  const labels = { badgeSchema: '表结构', badgeData: '数据', badgeTS: '时序' };
  const stateLabels = { pending: '等待', running: '进行中', done: '完成', error: '错误' };
  el.textContent = (labels[id] || '') + ' ' + (stateLabels[state] || state);
}

function appendLog(msg, cls = 'log-info') {
  const log = document.getElementById('migrationLog');
  const line = document.createElement('span');
  line.className = cls;
  line.textContent = '[' + new Date().toLocaleTimeString() + '] ' + msg + '\n';
  log.appendChild(line);
  log.scrollTop = log.scrollHeight;
}

function setProgress(pct, label) {
  document.getElementById('progressBar').style.width = pct + '%';
  document.getElementById('progressBar').setAttribute('aria-valuenow', pct);
  document.getElementById('progressPct').textContent = pct + '%';
  if (label) document.getElementById('progressLabel').textContent = label;
}

// Stage → progress percentage bucket
const STAGE_PCT = { schema: 10, index: 25, fk: 35, data: 65, timeseries: 95, done: 100 };

function handleProgress(p) {
  const stageName = STAGE_LABELS[p.stage] || p.stage;

  // Update badge
  if (p.stage === 'schema' || p.stage === 'index' || p.stage === 'fk') {
    setBadge('badgeSchema', p.stage === 'done' ? 'done' : 'running');
  } else if (p.stage === 'data') {
    setBadge('badgeData', 'running');
  } else if (p.stage === 'timeseries') {
    setBadge('badgeTS', 'running');
  } else if (p.stage === 'done') {
    setBadge('badgeSchema', 'done');
    setBadge('badgeData', 'done');
    setBadge('badgeTS', 'done');
  } else if (p.stage === 'error') {
    setBadge('badgeSchema', 'error');
    setBadge('badgeData', 'error');
    setBadge('badgeTS', 'error');
  }

  // Progress bar
  const basePct = STAGE_PCT[p.stage] || 0;
  let pct = basePct;
  if (p.total > 0) {
    const stagePct = STAGE_PCT[p.stage] || 0;
    const nextStage = Object.values(STAGE_PCT).find(v => v > stagePct) || 100;
    pct = Math.round(stagePct + ((nextStage - stagePct) * p.done / p.total));
  }
  if (p.stage === 'done') pct = 100;
  setProgress(pct, stageName);

  // Log
  const table = p.table ? ` [${p.table}]` : '';
  if (p.error) {
    appendLog(`❌ ${stageName}${table}: ${p.error}`, 'log-error');
  } else if (p.message) {
    const cls = p.stage === 'done' ? 'log-ok' : 'log-info';
    appendLog(`${stageName}${table}: ${p.message}`, cls);
  }
}

function startMigration(opts) {
  document.getElementById('migrationLog').innerHTML = '';
  ['badgeSchema', 'badgeData', 'badgeTS'].forEach(id => setBadge(id, 'pending'));
  setProgress(0, '连接数据库…');
  document.getElementById('btnStopMigration').style.display = 'inline-flex';
  document.getElementById('btnMigrationDone').classList.add('d-none');
  document.getElementById('btnMigrationRetry').classList.add('d-none');

  apiPost('/api/migrate/start', opts).then(data => {
    if (data.error) {
      appendLog('启动失败: ' + data.error, 'log-error');
      return;
    }
    window.AppState.taskID = data.task_id;
    appendLog(`任务已启动 (${data.task_id})`, 'log-ok');

    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const wsURL = `${proto}://${location.host}/api/migrate/progress?task_id=${data.task_id}`;
    wsConn = new WebSocket(wsURL);

    wsConn.onmessage = evt => {
      const p = JSON.parse(evt.data);
      handleProgress(p);
      if (p.stage === 'done') {
        document.getElementById('btnStopMigration').style.display = 'none';
        document.getElementById('btnMigrationDone').classList.remove('d-none');
        unlockStep(5);
        wsConn.close();
      } else if (p.stage === 'error' || p.status === 'error') {
        document.getElementById('btnStopMigration').style.display = 'none';
        document.getElementById('btnMigrationRetry').classList.remove('d-none');
        wsConn.close();
      }
    };
    wsConn.onerror = () => appendLog('WebSocket 错误', 'log-error');
    wsConn.onclose = () => appendLog('连接已关闭', 'log-warn');
  }).catch(e => appendLog('请求失败: ' + e.message, 'log-error'));
}

document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('btnStartMigration').addEventListener('click', () => {
    const cfg = window.AppState.config;
    if (!cfg) { alert('请先完成连接配置'); goToStep(1); return; }

    const opts = {
      config:               cfg,
      tenant_ids:           window.AppState.tenantIDs,
      migrate_schema:       document.getElementById('optSchema').checked,
      migrate_data:         document.getElementById('optData').checked,
      migrate_time_series:  document.getElementById('optTS').checked,
    };

    unlockStep(4);
    goToStep(4);
    startMigration(opts);
  });

  document.getElementById('btnStopMigration').addEventListener('click', () => {
    if (wsConn) wsConn.close();
    appendLog('用户已终止迁移', 'log-warn');
    document.getElementById('btnStopMigration').style.display = 'none';
    document.getElementById('btnMigrationRetry').classList.remove('d-none');
  });
});
