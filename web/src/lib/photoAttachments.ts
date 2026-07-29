const SUPPORTED_TYPES = new Set(["image/jpeg", "image/png", "image/gif", "image/webp"]);
export const MAX_PHOTO_BYTES = 10 * 1024 * 1024;
export const MAX_PHOTOS_PER_MESSAGE = 4;
const MAX_DIMENSION = 2000;

export interface NormalizedPhoto {
  file: File;
  visual: Blob;
  previewURL: string;
  width: number;
  height: number;
}

export async function normalizePhoto(file: File): Promise<NormalizedPhoto> {
  if (!SUPPORTED_TYPES.has(file.type)) throw new Error(`${file.name}: use JPEG, PNG, GIF, or WebP.`);
  if (file.size === 0) throw new Error(`${file.name}: the file is empty.`);
  if (file.size > MAX_PHOTO_BYTES) throw new Error(`${file.name}: photos must be 10 MiB or smaller.`);

  const bitmap = await loadBitmap(file);
  const scale = Math.min(1, MAX_DIMENSION / Math.max(bitmap.width, bitmap.height));
  const width = Math.max(1, Math.round(bitmap.width * scale));
  const height = Math.max(1, Math.round(bitmap.height * scale));
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d");
  if (!context) {
    bitmap.close();
    throw new Error(`${file.name}: this browser cannot process the image.`);
  }
  try {
    context.fillStyle = "#fff";
    context.fillRect(0, 0, width, height);
    context.drawImage(bitmap.source, 0, 0, width, height);
  } finally {
    bitmap.close();
  }
  const visual = await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error(`${file.name}: JPEG conversion failed.`))),
      "image/jpeg",
      0.85,
    );
  });
  return { file, visual, previewURL: URL.createObjectURL(visual), width, height };
}

async function loadBitmap(file: File): Promise<{ source: CanvasImageSource; width: number; height: number; close: () => void }> {
  if ("createImageBitmap" in window) {
    const image = await createImageBitmap(file);
    return { source: image, width: image.width, height: image.height, close: () => image.close() };
  }
  const url = URL.createObjectURL(file);
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image();
      element.onload = () => resolve(element);
      element.onerror = () => reject(new Error(`${file.name}: the browser could not decode this image.`));
      element.src = url;
    });
    return { source: image, width: image.naturalWidth, height: image.naturalHeight, close: () => {} };
  } finally {
    URL.revokeObjectURL(url);
  }
}
