import { test } from 'node:test';
import assert from 'node:assert/strict';
import { filterChats } from './history-chats.ts';
const chats = [
  { jid: '900000000001@lid', name: '900000000001' },
  { jid: 'group@g.us', name: 'Synthetic Team' },
  { jid: '900000000002@lid', name: 'Alice Example' },
  { jid: '900000000003@lid', name: '' },
  { jid: '900000000004@lid', name: 'സിന്തറ്റിക്' },
];
test('named conversations sort first without changing source or selections', () => {
  const before = structuredClone(chats);
  const selected = new Set([chats[0].jid, chats[1].jid]);
  const result = filterChats(chats, '');
  assert.equal(result[0].name, 'Alice Example');
  assert.ok(result.slice(0, 3).every(c => /[A-Za-z\u0D00-\u0D7F]/.test(c.name)));
  assert.deepEqual(chats, before);
  filterChats(chats, 'alice');
  assert.equal(selected.size, 2);
});
test('search supports case, whitespace, numbers, groups, Unicode and no matches', () => {
  assert.equal(filterChats(chats, '  ALICE  ')[0].jid, chats[2].jid);
  assert.equal(filterChats(chats, '000001')[0].jid, chats[0].jid);
  assert.equal(filterChats(chats, '+900 000 000 001')[0].jid, chats[0].jid);
  assert.equal(filterChats(chats, 'team')[0].jid, chats[1].jid);
  assert.equal(filterChats(chats, 'സിന്തറ്റിക്').length, 1);
  assert.equal(filterChats(chats, 'missing').length, 0);
});
