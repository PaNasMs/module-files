from pathlib import Path
import re


def default_umask(path=Path('/etc/login.defs')):
    try:
        text = path.read_text()
    except FileNotFoundError:
        return 0o022
    mask = 0o022
    for line in text.splitlines():
        fields = line.split('#', 1)[0].split()
        if not fields or fields[0] != 'UMASK':
            continue
        if len(fields) != 2 or not re.fullmatch(r'[0-7]+', fields[1]) or int(fields[1], 8) > 0o777:
            raise ValueError('Invalid UMASK in login.defs')
        mask = int(fields[1], 8)
    return mask
