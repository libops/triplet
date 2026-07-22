UPDATE iiif_presentation_resources
SET body = ?, updated_at = CURRENT_TIMESTAMP(6)
WHERE resource_key = ?
