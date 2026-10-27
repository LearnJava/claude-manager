// Translation fragment: learned (en). Keys are namespaced 'learned.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'learned.title': 'Learned files',
    'learned.scan': 'Compare',
    'learned.intro':
        'Skills, rules and bans live in the database; {dir} keeps them as plain files you can commit, revert with git and carry to another machine. Nothing is written into the project, and nothing is imported or retired, until you tick it and press "Apply". A version that differs on both sides is never overwritten without your choice.',
    'learned.offer': '{count} lesson(s) in the project files are not in the database (or differ from it) — tick them to take them over.',
    'learned.empty': 'The database and the files agree.',
    'learned.kind.skill': 'Skill',
    'learned.kind.rule': 'Rule',
    'learned.kind.ban': 'Ban',
    'learned.action.export_new': 'write to a file',
    'learned.action.export_upd': 'update the file (changed here)',
    'learned.action.import_new': 'import from the file',
    'learned.action.import_upd': 'import the changed file',
    'learned.action.retire': 'file removed — take out of force here',
    'learned.action.remove_file': 'taken out of force here — remove the file',
    'learned.action.conflict': 'conflict',
    'learned.status': 'here: {db} · file: {file}',
    'learned.choose': 'Choose a version…',
    'learned.useFile': 'Use the file',
    'learned.useDb': 'Use this machine',
    'learned.apply': 'Apply ({count})',
    'learned.applied': 'Done: {count}.',
};
