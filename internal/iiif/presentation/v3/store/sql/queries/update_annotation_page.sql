UPDATE iiif_presentation_annotation_pages
SET body = ?, updated_at = CURRENT_TIMESTAMP(6)
WHERE item_id = ? AND canvas_id = ?
