// 分片上传会话：工作台的参考图与 EPUB 都走 /api/uploads/sessions。
// 分片落在 local-data/uploads/<upload_id>/，完成后合并成一个媒体文件或一本书。
import fs from 'node:fs';
import path from 'node:path';

import {CHUNK_SIZE, TTS, UPLOAD_DIR} from './config.mjs';
import {nextId, nowIso, persist, saveMedia, store} from './store.mjs';
import {NotFoundError} from './errors.mjs';

export class UploadError extends Error {}

const AUDIO_EXTENSIONS = /\.(wav|mp3|flac|m4a|ogg|opus|aac)$/i;

function ttsAudioMime(filename, fallback = '') {
  const extension = (filename.match(/\.([a-z0-9]+)$/i) || [])[1]?.toLowerCase();
  const table = {wav: 'audio/wav', mp3: 'audio/mpeg', flac: 'audio/flac', m4a: 'audio/mp4', ogg: 'audio/ogg', opus: 'audio/ogg', aac: 'audio/aac'};
  return (extension && table[extension]) || fallback || 'audio/wav';
}

function sessionView(session) {
  return {
    upload_id: session.upload_id,
    kind: session.kind,
    filename: session.filename,
    content_type: session.content_type,
    chunk_size: session.chunk_size,
    total_size: session.total_size,
    total_chunks: session.total_chunks,
    uploaded_chunks: [...session.uploaded_chunks].sort((left, right) => left - right),
    status: session.status,
    expires_at: null,
    result: session.result || null,
  };
}

export function createSession(payload) {
  const kind = String(payload.kind || '');
  if (kind !== 'generation_reference_image' && kind !== 'novel_epub' && kind !== 'tts_ref_audio') {
    throw new UploadError('不支持的上传类型');
  }
  const totalSize = Number.parseInt(payload.total_size, 10);
  if (!Number.isFinite(totalSize) || totalSize <= 0) {
    throw new UploadError('上传内容为空');
  }
  if (kind === 'tts_ref_audio') {
    const maxBytes = TTS.maxRefAudioMb * 1024 * 1024;
    if (totalSize > maxBytes) {
      throw new UploadError(`参考音频超过 ${TTS.maxRefAudioMb}MB 上限`);
    }
  }
  const clientUploadId = String(payload.client_upload_id || '');

  if (clientUploadId) {
    const existing = Object.values(store.uploadSessions).find(
      (session) => session.client_upload_id === clientUploadId && session.status !== 'canceled',
    );
    if (existing) return sessionView(existing);
  }

  const id = `up-${nextId('upload')}-${Math.random().toString(36).slice(2, 8)}`;
  const session = {
    upload_id: id,
    client_upload_id: clientUploadId,
    kind,
    filename: String(payload.filename || '').trim() || (kind === 'novel_epub' ? 'novel.epub' : 'reference-image.png'),
    content_type: String(payload.content_type || 'application/octet-stream'),
    total_size: totalSize,
    chunk_size: CHUNK_SIZE,
    total_chunks: Math.max(1, Math.ceil(totalSize / CHUNK_SIZE)),
    uploaded_chunks: [],
    status: 'uploading',
    created_at: nowIso(),
    result: null,
  };
  store.uploadSessions[id] = session;
  fs.mkdirSync(path.join(UPLOAD_DIR, id), {recursive: true});
  persist();
  return sessionView(session);
}

export function findSession(uploadId) {
  return store.uploadSessions[uploadId] || null;
}

export function sessionJSON(uploadId) {
  const session = findSession(uploadId);
  if (!session) throw new NotFoundError('上传会话不存在或已过期');
  return sessionView(session);
}

export function saveChunk(uploadId, index, buffer) {
  const session = findSession(uploadId);
  if (!session) throw new NotFoundError('上传会话不存在或已过期');
  if (session.status === 'completed') return sessionView(session);
  if (session.status === 'canceled') throw new UploadError('上传会话已取消');
  if (!Number.isInteger(index) || index < 0 || index >= session.total_chunks) {
    throw new UploadError('分片序号超出范围');
  }
  fs.writeFileSync(path.join(UPLOAD_DIR, uploadId, `${index}.part`), buffer);
  if (!session.uploaded_chunks.includes(index)) {
    session.uploaded_chunks.push(index);
    persist();
  }
  return sessionView(session);
}

export function cancelSession(uploadId) {
  const session = findSession(uploadId);
  if (!session) throw new UploadError('上传会话不存在或已过期');
  session.status = 'canceled';
  fs.rmSync(path.join(UPLOAD_DIR, uploadId), {recursive: true, force: true});
  persist();
  return sessionView(session);
}

function assemble(session) {
  const directory = path.join(UPLOAD_DIR, session.upload_id);
  const parts = [];
  for (let index = 0; index < session.total_chunks; index += 1) {
    const file = path.join(directory, `${index}.part`);
    if (!fs.existsSync(file)) {
      throw new UploadError(`分片 ${index} 尚未上传完成`);
    }
    parts.push(fs.readFileSync(file));
  }
  const buffer = Buffer.concat(parts);
  fs.rmSync(directory, {recursive: true, force: true});
  return buffer;
}

/**
 * 完成上传：参考图登记成 media token，EPUB 落到 local-data/novel 并返回书对象。
 */
export async function completeSession(uploadId) {
  const session = findSession(uploadId);
  if (!session) throw new UploadError('上传会话不存在或已过期');
  if (session.status === 'completed') return sessionView(session);
  if (session.status === 'canceled') throw new UploadError('上传会话已取消');

  const buffer = assemble(session);
  session.status = 'completed';
  session.uploaded_chunks = Array.from({length: session.total_chunks}, (_, index) => index);

  if (session.kind === 'generation_reference_image') {
    const token = `ref-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`;
    const mediaToken = saveMedia(buffer, {
      mimeType: session.content_type || 'image/png',
      name: session.filename,
    });
    store.referenceUploads[token] = {
      mediaToken,
      filename: session.filename,
      created_at: nowIso(),
    };
    session.result = {
      reference_upload_token: token,
      reference_image_name: session.filename,
      reference_image_attached: true,
    };
  } else if (session.kind === 'tts_ref_audio') {
    if (!AUDIO_EXTENSIONS.test(session.filename)) {
      throw new UploadError('参考音频只支持 wav / mp3 / flac / m4a / ogg / opus / aac 格式');
    }
    const mediaToken = saveMedia(buffer, {
      mimeType: ttsAudioMime(session.filename, session.content_type),
      name: session.filename,
    });
    const token = `ttsref-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`;
    store.referenceUploads[token] = {
      kind: 'tts_ref_audio',
      mediaToken,
      filename: session.filename,
      created_at: nowIso(),
    };
    session.result = {
      reference_upload_token: token,
      reference_audio_name: session.filename,
      reference_audio_attached: true,
    };
  } else {
    // EPUB 走和 /api/novelviz/books/upload 同一条登记路径，避免两份书表逻辑。
    const {registerUploadedBook} = await import('./novel.mjs');
    session.result = registerUploadedBook({
      filename: session.filename,
      displayName: session.filename,
      buffer,
    });
  }
  persist();
  return sessionView(session);
}
