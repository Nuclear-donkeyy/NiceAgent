UPDATE skill_versions
SET input_schema = '{"type":"object","properties":{"action":{"type":"string","enum":["summary","list","read"],"description":"summary returns workspace artifact metadata; list returns artifacts; read returns text artifact content"},"artifact_id":{"type":"string"},"max_bytes":{"type":"integer","minimum":1,"maximum":262144}}}'::jsonb
WHERE id = 'skv_workspace_read_001'
  AND skill_id = 'workspace.read';
