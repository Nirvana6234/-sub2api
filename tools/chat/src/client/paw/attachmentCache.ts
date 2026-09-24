const DATABASE_NAME = "paw-attachments";
const DATABASE_VERSION = 1;
const STORE_NAME = "files";

interface StoredAttachment {
  id: string;
  blob: Blob;
}

function canUseIndexedDb(): boolean {
  return typeof window !== "undefined" && typeof window.indexedDB !== "undefined";
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("IndexedDB request failed"));
  });
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DATABASE_NAME, DATABASE_VERSION);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) {
        request.result.createObjectStore(STORE_NAME, { keyPath: "id" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("IndexedDB open failed"));
  });
}

export async function savePawAttachmentBlob(id: string, blob: Blob): Promise<boolean> {
  if (!id || !canUseIndexedDb()) return false;
  let database: IDBDatabase | null = null;
  try {
    database = await openDatabase();
    const transaction = database.transaction(STORE_NAME, "readwrite");
    transaction.objectStore(STORE_NAME).put({ id, blob } satisfies StoredAttachment);
    await waitForTransaction(transaction);
    return true;
  } catch {
    return false;
  } finally {
    database?.close();
  }
}

export async function loadPawAttachmentBlob(id: string): Promise<Blob | null> {
  if (!id || !canUseIndexedDb()) return null;
  let database: IDBDatabase | null = null;
  try {
    database = await openDatabase();
    const result = await requestResult(
      database.transaction(STORE_NAME, "readonly").objectStore(STORE_NAME).get(id),
    );
    return result && result.blob instanceof Blob ? result.blob : null;
  } catch {
    return null;
  } finally {
    database?.close();
  }
}

export async function deletePawAttachmentBlob(id: string): Promise<void> {
  if (!id || !canUseIndexedDb()) return;
  let database: IDBDatabase | null = null;
  try {
    database = await openDatabase();
    const transaction = database.transaction(STORE_NAME, "readwrite");
    transaction.objectStore(STORE_NAME).delete(id);
    await waitForTransaction(transaction);
  } catch {
    // Local attachment cleanup is best effort; the server attachment is separate.
  } finally {
    database?.close();
  }
}

export async function clearPawAttachmentCache(): Promise<void> {
  if (!canUseIndexedDb()) return;
  let database: IDBDatabase | null = null;
  try {
    database = await openDatabase();
    const transaction = database.transaction(STORE_NAME, "readwrite");
    transaction.objectStore(STORE_NAME).clear();
    await waitForTransaction(transaction);
  } catch {
    // Local attachment cleanup is best effort.
  } finally {
    database?.close();
  }
}

function waitForTransaction(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () =>
      reject(transaction.error ?? new Error("IndexedDB transaction failed"));
    transaction.onabort = () =>
      reject(transaction.error ?? new Error("IndexedDB transaction aborted"));
  });
}
