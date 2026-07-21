SELECT body
FROM iiif_presentation_resources
WHERE resource_key = ?
FOR UPDATE
