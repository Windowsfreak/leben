<?php
session_start();
header('Content-Type: application/json');

// Set time limit for LLM translation actions
set_time_limit(300);
ini_set('max_execution_time', '300');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// All tile admin actions require authentication
$is_admin = !empty($_SESSION['admin_logged_in']);
if (!$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    $db = get_db_connection();

    switch ($action) {
        case 'get_tiles':
            $stmt = $db->prepare("SELECT id, name, language, title, html_teaser, summary, tags, type, link, content_file, visible, secret, accent_color, background, sort_order, created_at, updated_at FROM tiles ORDER BY name ASC, language ASC");
            $stmt->execute();
            $tiles = $stmt->fetchAll(PDO::FETCH_ASSOC);
            foreach ($tiles as &$t) {
                $raw_tags = trim((string)($t['tags'] ?? ''), '{}');
                $t['tags'] = array_values(array_filter(array_map('trim', explode(',', $raw_tags))));
            }
            echo json_encode([
                'status' => 'success',
                'tiles' => $tiles
            ]);
            break;

        case 'translation_status':
            $tile_name = $_GET['name'] ?? $_POST['name'] ?? null;
            $matrix = get_translation_status($tile_name);
            echo json_encode([
                'status' => 'success',
                'data' => $matrix
            ]);
            break;

        case 'auto_translate':
            $name = trim($_POST['name'] ?? '');
            $target_lang = strtolower(trim($_POST['target_lang'] ?? 'all'));

            if (empty($name)) {
                throw new Exception("Tile name is required for translation.");
            }

            $res = execute_auto_translate($name, $target_lang);

            $count = count($res['translated']);
            $langs_str = implode(', ', array_map(function($r) { return strtoupper($r['language']); }, $res['translated']));

            echo json_encode([
                'status' => 'success',
                'message' => $count > 0 
                    ? "Kachel '{$name}' wurde erfolgreich in {$count} Sprache(n) ({$langs_str}) übersetzt." 
                    : "Kachel '{$name}' ist bereits für alle unterstützten Sprachen vorhanden.",
                'data' => $res
            ]);
            break;

        case 'save':
            $id = isset($_POST['id']) && $_POST['id'] !== '' ? (int)$_POST['id'] : null;
            $name = trim($_POST['name'] ?? '');
            $language = trim($_POST['language'] ?? 'de');

            if (!$id && !empty($name) && !empty($language)) {
                $stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :language LIMIT 1");
                $stmt->execute([':name' => $name, ':language' => $language]);
                $found = $stmt->fetch();
                if ($found) {
                    $id = (int)$found['id'];
                }
            }
            $title = trim($_POST['title'] ?? '');
            $html_teaser = trim($_POST['html_teaser'] ?? '');
            $summary = trim($_POST['summary'] ?? '');
            $link = trim($_POST['link'] ?? '');
            $type = trim($_POST['type'] ?? 'doc');
            $content_file = trim($_POST['content_file'] ?? '');
            $visible = ($_POST['visible'] ?? 'true') === 'true';
            $secret = trim($_POST['secret'] ?? '');
            $sort_order = isset($_POST['sort_order']) ? (int)$_POST['sort_order'] : 100;
            $accent_color = trim($_POST['accent_color'] ?? '#fbbf24');
            $background = trim($_POST['background'] ?? '');
            
            // Clean categories array from comma-separated list
            $categories_input = $_POST['tags'] ?? '';
            $tags = array_filter(array_map('trim', explode(',', $categories_input)));
            $pg_tags = '{' . implode(',', $tags) . '}';

            if (empty($title) && $type === 'link') {
                $title = $name;
            }

            if (empty($name) || empty($language) || empty($title)) {
                throw new Exception("Name and language are required fields.");
            }

            // Check if embedding needs regeneration
            $regenerate_embedding = true;
            $existing = null;

            if ($id) {
                $stmt = $db->prepare("SELECT * FROM tiles WHERE id = :id");
                $stmt->execute([':id' => $id]);
                $existing = $stmt->fetch();
                
                if ($existing) {
                    // Compare tags cleanly (compare arrays)
                    $existing_tags_str = trim($existing['tags'] ?? '', '{}');
                    $existing_tags = array_filter(array_map('trim', explode(',', $existing_tags_str)));
                    sort($existing_tags);
                    $new_tags = $tags;
                    sort($new_tags);
                    $existing_summary = $existing['summary'] ?? '';
                    
                    if (
                        $existing['name'] === $name &&
                        $existing['language'] === $language &&
                        $existing_summary === $summary &&
                        $existing_tags === $new_tags
                    ) {
                        $regenerate_embedding = false;
                    }
                }
            }

            $vector_str = null;
            if ($regenerate_embedding) {
                // Query embedding from Ollama using structured text format
                try {
                    $doc_text = format_tile_document_text($name, $language, $tags, $summary);
                    $embedding = get_embedding($doc_text, 'document');
                    $vector_str = array_to_postgres_vector($embedding);
                } catch (Exception $e) {
                    // If update fails due to embedding server, and we have an existing vector, keep it
                    if ($existing && !empty($existing['embedding'])) {
                        $vector_str = $existing['embedding'];
                    } else {
                        // Fallback to zero vector
                        $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
                    }
                    error_log("Admin Save: embedding failed. " . $e->getMessage());
                }
            } else {
                // Keep the old embedding vector
                $vector_str = $existing['embedding'];
            }

            if ($id) {
                // Update
                $sql = "
                    UPDATE tiles 
                    SET name = :name, 
                        language = :language, 
                        tags = :tags, 
                        title = :title, 
                        html_teaser = :html_teaser, 
                        summary = :summary, 
                        link = :link, 
                        type = :type, 
                        content_file = :content_file, 
                        visible = :visible, 
                        secret = :secret,
                        accent_color = :accent_color,
                        background = :background,
                        embedding = :embedding::vector, 
                        sort_order = :sort_order,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = :id
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':id', $id, PDO::PARAM_INT);
            } else {
                // Insert
                $sql = "
                    INSERT INTO tiles (
                        name, language, tags, title, html_teaser, 
                        summary, link, type, content_file, 
                        visible, secret, accent_color, background, embedding, sort_order
                    ) VALUES (
                        :name, :language, :tags, :title, :html_teaser, 
                        :summary, :link, :type, :content_file, 
                        :visible, :secret, :accent_color, :background, :embedding::vector, :sort_order
                    )
                ";
                $stmt = $db->prepare($sql);
            }

            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':language', $language, PDO::PARAM_STR);
            $stmt->bindValue(':tags', $pg_tags, PDO::PARAM_STR);
            $stmt->bindValue(':title', $title, PDO::PARAM_STR);
            $stmt->bindValue(':html_teaser', $html_teaser, PDO::PARAM_STR);
            $stmt->bindValue(':summary', $summary, PDO::PARAM_STR);
            $stmt->bindValue(':link', $link ?: null, PDO::PARAM_STR);
            $stmt->bindValue(':type', $type, PDO::PARAM_STR);
            $stmt->bindValue(':content_file', $content_file ?: null, PDO::PARAM_STR);
            $stmt->bindValue(':visible', $visible, PDO::PARAM_BOOL);
            $stmt->bindValue(':secret', $secret, PDO::PARAM_STR);
            $stmt->bindValue(':accent_color', $accent_color, PDO::PARAM_STR);
            $stmt->bindValue(':background', $background ?: null, PDO::PARAM_STR);
            $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
            $stmt->bindValue(':sort_order', $sort_order, PDO::PARAM_INT);
            $stmt->execute();

            $sync_siblings = ($_POST['sync_siblings'] ?? 'false') === 'true' || ($_POST['sync_siblings'] ?? '0') === '1';
            $synced_count = 0;

            if ($sync_siblings) {
                $saved_id = $id;
                if (!$saved_id) {
                    $id_stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :language LIMIT 1");
                    $id_stmt->execute([':name' => $name, ':language' => $language]);
                    $saved_row = $id_stmt->fetch();
                    $saved_id = $saved_row ? (int)$saved_row['id'] : 0;
                }

                $sync_stmt = $db->prepare("
                    UPDATE tiles 
                    SET type = :type,
                        link = :link,
                        visible = :visible,
                        secret = :secret,
                        accent_color = :accent_color,
                        background = :background,
                        sort_order = :sort_order,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE name = :name AND id != :saved_id
                ");
                $sync_stmt->bindValue(':type', $type, PDO::PARAM_STR);
                $sync_stmt->bindValue(':link', $link ?: null, PDO::PARAM_STR);
                $sync_stmt->bindValue(':visible', $visible, PDO::PARAM_BOOL);
                $sync_stmt->bindValue(':secret', $secret, PDO::PARAM_STR);
                $sync_stmt->bindValue(':accent_color', $accent_color, PDO::PARAM_STR);
                $sync_stmt->bindValue(':background', $background ?: null, PDO::PARAM_STR);
                $sync_stmt->bindValue(':sort_order', $sort_order, PDO::PARAM_INT);
                $sync_stmt->bindValue(':name', $name, PDO::PARAM_STR);
                $sync_stmt->bindValue(':saved_id', $saved_id, PDO::PARAM_INT);
                $sync_stmt->execute();
                $synced_count = $sync_stmt->rowCount();
            }

            echo json_encode([
                'status' => 'success',
                'message' => $sync_siblings 
                    ? "Kachel gespeichert und Einstellungen auf {$synced_count} Geschwister-Kachel(n) übertragen." 
                    : 'Kachel erfolgreich gespeichert.',
                'synced_count' => $synced_count
            ]);
            break;

        case 'delete':
            $id = isset($_POST['id']) ? (int)$_POST['id'] : null;
            if (!$id) {
                throw new Exception("Tile ID is required for deletion.");
            }

            $stmt = $db->prepare("DELETE FROM tiles WHERE id = :id");
            $stmt->execute([':id' => $id]);

            echo json_encode([
                'status' => 'success',
                'message' => 'Tile deleted successfully.'
            ]);
            break;

        case 'clone':
            $id = isset($_POST['id']) ? (int)$_POST['id'] : null;
            if (!$id) {
                throw new Exception("Tile ID is required for cloning.");
            }

            // Fetch current tile
            $stmt = $db->prepare("SELECT * FROM tiles WHERE id = :id");
            $stmt->execute([':id' => $id]);
            $tile = $stmt->fetch();

            if (!$tile) {
                throw new Exception("Source tile not found.");
            }

            // Create cloned version with suffix/alternate lang
            $clone_name = $tile['name'] . '_copy';
            $clone_title = $tile['title'] . ' (Copy)';
            
            // Check if name + language combination exists
            $check_stmt = $db->prepare("SELECT 1 FROM tiles WHERE name = :name AND language = :language");
            $check_stmt->execute([':name' => $clone_name, ':language' => $tile['language']]);
            $counter = 1;
            while ($check_stmt->fetch()) {
                $clone_name = $tile['name'] . '_copy' . $counter;
                $check_stmt->execute([':name' => $clone_name, ':language' => $tile['language']]);
                $counter++;
            }

            $src_type = $tile['type'] ?? 'doc';
            $src_tags = $tile['tags'] ?? '';
            $src_summary = $tile['summary'] ?? '';

            $sql = "
                INSERT INTO tiles (
                    name, language, tags, title, html_teaser, 
                    summary, link, type, content_file, 
                    visible, secret, accent_color, background, embedding, sort_order
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser, 
                    :summary, :link, :type, :content_file, 
                    :visible, :secret, :accent_color, :background, :embedding::vector, :sort_order
                )
            ";

            $insert = $db->prepare($sql);
            $insert->bindValue(':name', $clone_name, PDO::PARAM_STR);
            $insert->bindValue(':language', $tile['language'], PDO::PARAM_STR);
            $insert->bindValue(':tags', $src_tags, PDO::PARAM_STR);
            $insert->bindValue(':title', $clone_title, PDO::PARAM_STR);
            $insert->bindValue(':html_teaser', $tile['html_teaser'], PDO::PARAM_STR);
            $insert->bindValue(':summary', $src_summary, PDO::PARAM_STR);
            $insert->bindValue(':link', $tile['link'], PDO::PARAM_STR);
            $insert->bindValue(':type', $src_type, PDO::PARAM_STR);
            $insert->bindValue(':content_file', $tile['content_file'], PDO::PARAM_STR);
            $insert->bindValue(':visible', false, PDO::PARAM_BOOL); // Set cloned copy to invisible by default
            $insert->bindValue(':secret', $tile['secret'] ?? '', PDO::PARAM_STR);
            $insert->bindValue(':accent_color', $tile['accent_color'], PDO::PARAM_STR);
            $insert->bindValue(':background', $tile['background'], PDO::PARAM_STR);
            $insert->bindValue(':embedding', $tile['embedding'], PDO::PARAM_STR);
            $insert->bindValue(':sort_order', $tile['sort_order'], PDO::PARAM_INT);
            $insert->execute();

            echo json_encode([
                'status' => 'success',
                'message' => 'Tile cloned successfully.',
                'clone_name' => $clone_name
            ]);
            break;

        case 'toggle_visibility':
            $id = isset($_POST['id']) ? (int)$_POST['id'] : null;
            if (!$id) {
                throw new Exception("Tile ID is required.");
            }

            $stmt = $db->prepare("UPDATE tiles SET visible = NOT visible WHERE id = :id");
            $stmt->execute([':id' => $id]);

            echo json_encode([
                'status' => 'success',
                'message' => 'Tile visibility updated.'
            ]);
            break;

        case 'refresh_vectors':
            // Fetch all tiles
            $stmt = $db->prepare("SELECT id, name, language, tags, summary FROM tiles");
            $stmt->execute();
            $tiles = $stmt->fetchAll();

            $update_stmt = $db->prepare("UPDATE tiles SET embedding = :embedding::vector, updated_at = CURRENT_TIMESTAMP WHERE id = :id");
            $count = 0;

            foreach ($tiles as $tile) {
                // Parse tags
                $t_tags = $tile['tags'] ?? '';
                $raw_tags = trim((string)$t_tags, '{}');
                $tags_arr = array_filter(array_map('trim', explode(',', $raw_tags)));
                $t_summary = $tile['summary'] ?? '';
                
                // Format doc text using structured template
                $doc_text = format_tile_document_text($tile['name'], $tile['language'], $tags_arr, $t_summary);
                
                // Embed via Ollama
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);

                // Update database row
                $update_stmt->execute([
                    ':id' => $tile['id'],
                    ':embedding' => $vector_str
                ]);
                $count++;
            }

            echo json_encode([
                'status' => 'success',
                'message' => "Successfully regenerated vector embeddings for {$count} tiles."
            ]);
            break;

        case 'save_tile_html':
            $id = isset($_POST['id']) ? (int)$_POST['id'] : null;
            $html_teaser = $_POST['html_teaser'] ?? '';
            if (!$id) {
                throw new Exception("Tile ID is required.");
            }
            $stmt = $db->prepare("UPDATE tiles SET html_teaser = :html_teaser, updated_at = CURRENT_TIMESTAMP WHERE id = :id");
            $stmt->execute([':html_teaser' => $html_teaser, ':id' => $id]);
            echo json_encode(['status' => 'success', 'message' => 'Tile HTML updated successfully.']);
            break;

        default:
            throw new Exception("Invalid tile administration action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
