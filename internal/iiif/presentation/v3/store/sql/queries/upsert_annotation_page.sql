INSERT INTO iiif_presentation_annotation_pages (item_id, canvas_id, body)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE body = VALUES(body), updated_at = CURRENT_TIMESTAMP(6)
