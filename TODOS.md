- Check if abs can have automatic regular snapshots/backups
- when deleting a node, how to warn users of funnels that rely on the node
- add a page with explanations about huJSON for end users
- stress tests
- if all headscale instances run in the same process, what are error conditions
  that would bring the whole stack down (for example, does a derp map request
  failure bring down the whole process?)
- it's bad how everything uses the same logger. Logs for backups are mixed with
http request logs. Should logs have a "component" field to filter by?
- acl diff is hard to read on dark theme
- for the funnel machine, which has internal tag, add an icon (question mark, book)
