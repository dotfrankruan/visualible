# Visualible event callback plugin for Ansible.
#
# Loaded as an aggregate callback (CALLBACK_TYPE = 'aggregate') alongside
# the user's normal stdout callback, so it never changes what a human
# sees in a terminal. It appends one JSON object per line to the file
# named by VISUALIBLE_EVENT_FILE; the Go backend tails that file and
# normalizes events into the Visualible deployment event model.
#
# Uses only the stable ansible callback API (v2_* hooks).

import json
import os
import time

from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'aggregate'
    CALLBACK_NAME = 'visualible_events'
    CALLBACK_NEEDS_WHITELIST = False

    def __init__(self):
        super().__init__()
        self._path = os.environ.get('VISUALIBLE_EVENT_FILE')
        self._play = None

    # -- internal -----------------------------------------------------

    def _emit(self, event, **fields):
        if not self._path:
            return
        fields['event'] = event
        fields['ts'] = time.time()
        if self._play is not None:
            fields.setdefault('play', self._play)
        try:
            line = json.dumps(fields, default=str)
        except Exception:
            line = json.dumps({'event': event, 'ts': time.time()})
        try:
            with open(self._path, 'a', encoding='utf-8') as f:
                f.write(line + '\n')
        except OSError:
            pass

    @staticmethod
    def _task_name(task):
        try:
            return task.get_name().strip()
        except Exception:
            return ''

    # -- playbook / play lifecycle ------------------------------------

    def v2_playbook_on_start(self, playbook):
        self._emit('playbook.start',
                   playbook=os.path.basename(getattr(playbook, '_file_name', '') or ''))

    def v2_playbook_on_play_start(self, play):
        self._play = getattr(play, 'name', None) or ''
        self._emit('play.start', play=self._play)

    def v2_playbook_on_stats(self, stats):
        summary = {}
        try:
            for host in sorted(stats.processed.keys()):
                summary[host] = stats.summarize(host)
        except Exception:
            pass
        self._emit('stats', summary=summary)

    # -- task lifecycle ------------------------------------------------

    def v2_playbook_on_task_start(self, task, is_conditional):
        self._emit('task.start', task=self._task_name(task),
                   is_handler=False)

    def v2_playbook_on_handler_task_start(self, task):
        self._emit('task.start', task=self._task_name(task),
                   is_handler=True)

    # -- runner results -------------------------------------------------

    def _runner(self, event, result, changed=None):
        r = getattr(result, '_result', {}) or {}
        host = getattr(getattr(result, '_host', None), 'name', '')
        task = self._task_name(getattr(result, '_task', None))
        fields = {
            'task': task,
            'host': host,
        }
        if changed is not None:
            fields['changed'] = bool(changed)
        msg = r.get('msg')
        if msg:
            fields['message'] = str(msg)[:2000]
        self._emit(event, **fields)

    def v2_runner_on_ok(self, result):
        r = getattr(result, '_result', {}) or {}
        self._runner('task.ok' if not r.get('changed') else 'task.changed',
                     result, changed=bool(r.get('changed')))

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._runner('task.failed', result)

    def v2_runner_on_skipped(self, result):
        self._runner('task.skipped', result)

    def v2_runner_on_unreachable(self, result):
        self._runner('host.unreachable', result)
