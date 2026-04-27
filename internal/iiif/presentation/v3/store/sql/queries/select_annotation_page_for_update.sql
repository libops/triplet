SELECT body FROM iiif_presentation_annotation_pages
WHERE item_id = ? AND canvas_id = ?
FOR UPDATE
