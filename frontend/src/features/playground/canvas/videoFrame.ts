export type CanvasVideoFramePosition = 'first' | 'current' | 'last'

export async function captureCanvasVideoFrame(source: string, position: CanvasVideoFramePosition, currentTime = 0): Promise<Blob> {
  const video = document.createElement('video')
  video.crossOrigin = 'anonymous'
  video.muted = true
  video.playsInline = true
  video.preload = 'auto'
  try {
    const metadataLoaded = waitForVideoEvent(video, 'loadedmetadata')
    video.src = source
    video.load()
    await metadataLoaded
    if (video.videoWidth <= 0 || video.videoHeight <= 0) throw new Error('视频没有可用的画面尺寸。')
    const endTime = Math.max(0, Number.isFinite(video.duration) ? video.duration - 0.001 : 0)
    const targetTime = position === 'first' ? 0 : position === 'last' ? endTime : Math.max(0, Math.min(Number.isFinite(currentTime) ? currentTime : 0, endTime))
    if (targetTime > 0) {
      const seeked = waitForVideoEvent(video, 'seeked')
      video.currentTime = targetTime
      await seeked
    } else if (video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) {
      await waitForVideoEvent(video, 'loadeddata')
    }
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    const context = canvas.getContext('2d')
    if (!context) throw new Error('无法创建视频帧画布。')
    context.drawImage(video, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error('无法导出视频帧。')), 'image/png')
    })
  } finally {
    video.removeAttribute('src')
    video.load()
  }
}

function waitForVideoEvent(video: HTMLVideoElement, eventName: 'loadedmetadata' | 'loadeddata' | 'seeked'): Promise<void> {
  return new Promise((resolve, reject) => {
    const finish = () => {
      video.removeEventListener(eventName, finish)
      video.removeEventListener('error', fail)
      resolve()
    }
    const fail = () => {
      video.removeEventListener(eventName, finish)
      video.removeEventListener('error', fail)
      reject(new Error('无法读取视频画面。'))
    }
    video.addEventListener(eventName, finish)
    video.addEventListener('error', fail)
  })
}
