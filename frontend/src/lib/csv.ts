// csvField escapes a single CSV field per RFC 4180: wrap in quotes (doubling
// any internal quotes) whenever the value contains a comma, quote, or
// newline.
function csvField(value: string): string {
  if (/[",\n]/.test(value)) {
    return '"' + value.replace(/"/g, '""') + '"'
  }
  return value
}

// toCsv turns a header row and data rows into an RFC 4180 CSV string.
export function toCsv(header: string[], rows: string[][]): string {
  return [header, ...rows].map((row) => row.map(csvField).join(',')).join('\r\n') + '\r\n'
}

// downloadCsv triggers a browser download of the given CSV content.
export function downloadCsv(filename: string, csv: string) {
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
