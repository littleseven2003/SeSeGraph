import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseMetaText, splitTags } from '../src/utils/meta.ts'

test('首行标题模式：首行标题、其余为说明', () => {
  const meta = parseMetaText('夏日海边写真\n第一段说明\n第二段说明', 'first-line')
  assert.equal(meta.title, '夏日海边写真')
  assert.equal(meta.description, '第一段说明\n第二段说明')
  assert.deepEqual(meta.tags, [])
})

test('首行标题模式跳过开头空行与 BOM', () => {
  const meta = parseMetaText('\uFEFF\n\n标题行\n正文', 'first-line')
  assert.equal(meta.title, '标题行')
  assert.equal(meta.description, '正文')
})

test('键值模式解析标题、说明、标签与作者', () => {
  const text = ['标题：秋日登山', '说明：清晨出发的记录', '标签：户外, 山景，旅行', '作者：小林'].join('\n')
  const meta = parseMetaText(text, 'key-value')
  assert.equal(meta.title, '秋日登山')
  assert.equal(meta.description, '清晨出发的记录')
  assert.deepEqual(meta.tags, ['户外', '山景', '旅行'])
  assert.equal(meta.author, '小林')
})

test('键值模式支持英文键与半角冒号', () => {
  const meta = parseMetaText('title: Autumn\ntags: outdoor #hike', 'key-value')
  assert.equal(meta.title, 'Autumn')
  assert.deepEqual(meta.tags, ['outdoor', 'hike'])
})

test('键值模式无命中时回退首行标题', () => {
  const meta = parseMetaText('随便一行文字\n后续内容', 'key-value')
  assert.equal(meta.title, '随便一行文字')
  assert.equal(meta.description, '后续内容')
})

test('空文本不报错，返回空元信息', () => {
  assert.deepEqual(parseMetaText('', 'key-value'), { title: '', description: '', tags: [], author: '' })
  assert.deepEqual(parseMetaText('   \n  ', 'first-line'), { title: '', description: '', tags: [], author: '' })
})

test('标签去重并忽略空白项', () => {
  assert.deepEqual(splitTags('写真, 写真 ,  ，海边'), ['写真', '海边'])
})
