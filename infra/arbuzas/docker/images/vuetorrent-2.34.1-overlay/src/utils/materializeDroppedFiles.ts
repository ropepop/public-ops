export async function materializeDroppedFiles(files: File[]): Promise<File[]> {
  return Promise.all(
    files.map(async file => {
      const contents = await file.arrayBuffer()
      if (contents.byteLength !== file.size) {
        throw new Error('Dropped file contents do not match the advertised size')
      }

      return new File([contents], file.name, {
        type: file.type,
        lastModified: file.lastModified,
      })
    })
  )
}
