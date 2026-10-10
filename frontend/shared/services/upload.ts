import { apiRequest } from './client';

export interface UploadSession {
  upload_id: string;
  chunk_size: number;
  total_chunks: number;
  filename: string;
}

export async function createUploadSession(payload: {
  filename: string;
  file_size: number;
  chunk_size?: number;
  purpose?: string;
}): Promise<UploadSession> {
  return apiRequest<UploadSession>('/api/uploads/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function uploadChunk(
  uploadId: string,
  chunkIndex: number,
  chunkData: Blob
): Promise<{ success: boolean }> {
  return apiRequest(`/api/uploads/sessions/${encodeURIComponent(uploadId)}/chunks/${chunkIndex}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/octet-stream' },
    body: chunkData,
  });
}

export async function completeUpload(
  uploadId: string
): Promise<{ upload_token: string; filename: string }> {
  return apiRequest(`/api/uploads/sessions/${encodeURIComponent(uploadId)}/complete`, {
    method: 'POST',
  });
}

export async function uploadFileInChunks(
  file: File,
  purpose: string = 'reference_image',
  onProgress?: (percent: number) => void
): Promise<string> {
  const chunkSize = 4 * 1024 * 1024; // 4MB
  const session = await createUploadSession({
    filename: file.name,
    file_size: file.size,
    chunk_size: chunkSize,
    purpose,
  });

  const totalChunks = Math.ceil(file.size / chunkSize);
  for (let i = 0; i < totalChunks; i++) {
    const start = i * chunkSize;
    const end = Math.min(start + chunkSize, file.size);
    const chunk = file.slice(start, end);

    await uploadChunk(session.upload_id, i, chunk);
    if (onProgress) {
      onProgress(Math.round(((i + 1) / totalChunks) * 100));
    }
  }

  const result = await completeUpload(session.upload_id);
  return result.upload_token;
}
