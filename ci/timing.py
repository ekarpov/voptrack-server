"""Sanitized phase metrics: never accept request URLs, principals or payloads."""
from contextlib import contextmanager
import json
import os
import re
import time
from datetime import timezone
from email.utils import parsedate_to_datetime

LABEL = re.compile(r'^[a-z][a-z0-9-]{0,63}$')
PHASES = {'http-request','auth-backoff','setup-session-reused','setup-authorization'} | {p+s for p in ('bootstrap','restore','export','verification','cleanup','seed','token') for s in ('','-start')}

def event(name, seconds=0, **fields):
    if name not in PHASES or not LABEL.fullmatch(name) or set(fields) - {'attempt', 'status'}:
        raise ValueError('invalid metric fields')
    if any(type(v) not in (int, float) for v in fields.values()):
        raise ValueError('metrics accept numeric fields only')
    value = {'phase': name, 'seconds': round(float(seconds), 4), **fields}
    if not 0 <= value['seconds'] <= 86400:
        raise ValueError('invalid metric duration')
    path = os.environ.get('VOPTRACK_CI_METRICS_FILE')
    if path:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
        try:
            os.write(fd, (json.dumps(value, sort_keys=True) + '\n').encode())
        finally:
            os.close(fd)

@contextmanager
def phase(name):
    started = time.monotonic()
    try:
        yield
    finally:
        event(name, time.monotonic() - started)

class RetryBudget:
    """At most two retries, bounded by both server advice and elapsed time."""
    def __init__(self, attempts=2, deadline=130, clock=time.monotonic, sleep=time.sleep, wall=time.time):
        self.attempts, self.deadline = attempts, deadline
        self.clock, self.sleep, self.wall = clock, sleep, wall
        self.started = clock()

    def wait(self, headers, attempt):
        if attempt >= self.attempts:
            return False
        value = headers.get('Retry-After', '60')
        try:
            delay = float(int(value))
        except (ValueError, TypeError):
            try:
                date = parsedate_to_datetime(value)
                if date.tzinfo is None:
                    date = date.replace(tzinfo=timezone.utc)
                delay = date.timestamp() - self.wall()
            except (ValueError, TypeError, OverflowError):
                delay = 60
        delay = max(0.05, delay + 0.05)
        if delay > 65 or self.clock() - self.started + delay > self.deadline:
            return False
        print(f'  auth-backoff: retry {attempt + 1}, {delay:.2f}s', flush=True)
        started = self.clock()
        self.sleep(delay)
        event('auth-backoff', self.clock() - started, attempt=attempt + 1)
        return True

def timed_request(function):
    from functools import wraps
    @wraps(function)
    def wrapped(*args, **kwargs):
        started = time.monotonic()
        status = 599
        try:
            result = function(*args, **kwargs)
            status = int(result[0])
            return result
        finally:
            event('http-request', time.monotonic() - started, status=status)
    return wrapped

def retry_auth(function):
    """Only retry the existing browser-sign-in route; no other API is retried."""
    from functools import wraps
    from urllib.parse import urlsplit
    timed = timed_request(function)
    @wraps(function)
    def wrapped(*args, **kwargs):
        method = args[1] if len(args) > 1 else kwargs['method']
        url = args[2] if len(args) > 2 else kwargs['url']
        budget = RetryBudget()
        for attempt in range(3):
            result = timed(*args, **kwargs)
            if result[0] != 429 or method != 'POST' or urlsplit(url).path != '/oauth/authorize':
                return result
            if not budget.wait(result[2], attempt):
                return result
    return wrapped

if __name__ == '__main__':
    import sys
    event(sys.argv[1], float(sys.argv[2]), status=int(sys.argv[3]))
