<?php
session_start();
header('Content-Type: application/json');

// Set time limit for LLM translation actions
set_time_limit(300);
ini_set('max_execution_time', '300');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// Check authentication for administrative actions (except login/status check)
$needs_auth = !in_array($action, ['login', 'status', 'logout']);
$is_admin = !empty($_SESSION['admin_logged_in']);

if ($needs_auth && !$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    $db = get_db_connection();

    switch ($action) {
        case 'status':
            echo json_encode([
                'status' => 'success',
                'logged_in' => $is_admin
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
            $target_lang = strtolower(trim($_POST['target_lang'] ?? 'en'));
            $target_lang = in_array($target_lang, ['de', 'en']) ? $target_lang : 'en';

            if (empty($name)) {
                throw new Exception("Tile name is required for translation.");
            }

            // Execute translation using llm_admin logic
            $stmt = $db->prepare("SELECT * FROM tiles WHERE name = :name ORDER BY created_at ASC LIMIT 1");
            $stmt->execute([':name' => $name]);
            $source_tile = $stmt->fetch();

            if (!$source_tile) {
                throw new Exception("Source card '{$name}' not found.");
            }

            $source_lang = $source_tile['language'];
            if ($source_lang === $target_lang) {
                throw new Exception("Target language ('{$target_lang}') is identical to source language ('{$source_lang}').");
            }

            $contents_dir = __DIR__ . '/content/';
            $source_content = '';
            if (!empty($source_tile['content_file'])) {
                $fpath = $contents_dir . $source_tile['content_file'];
                if (file_exists($fpath)) {
                    $source_content = file_get_contents($fpath);
                }
            }
            if (empty($source_content)) {
                $source_content = $source_tile['html_teaser'];
            }

            $raw_tags = trim($source_tile['tags'], '{}');
            $tags_arr = array_filter(array_map('trim', explode(',', $raw_tags)));

            $lang_full_name = ($target_lang === 'en') ? 'English' : 'German';
            $meta_prompt = "You are an expert content translator. Translate the following tile metadata from " . strtoupper($source_lang) . " into {$lang_full_name}.
Respond ONLY with a valid JSON object (no markdown, no backticks):
{
  \"title\": \"Translated Title\",
  \"summary\": \"Translated summary...\",
  \"tags\": [\"tag1\", \"tag2\"]
}";
            $meta_user = "Source Title: {$source_tile['title']}\nSource Tags: " . implode(', ', $tags_arr) . "\nSource Summary: {$source_tile['summary']}";

            $meta_response = call_llm($meta_prompt, $meta_user);
            $meta_response = trim($meta_response);
            if (strpos($meta_response, '```') !== false) {
                $meta_response = preg_replace('/^```(?:json)?|```$/m', '', $meta_response);
                $meta_response = trim($meta_response);
            }
            $translated_meta = json_decode($meta_response, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("LLM metadata translation failed: " . $meta_response);
            }

            $html_prompt = "You are an expert HTML translator. Translate all human-readable text in the provided HTML snippet into {$lang_full_name}.
Keep all HTML tags, structure, classes, IDs, icons (<i class=\"...\"></i>), and attributes intact.
Respond ONLY with the translated HTML string. Do not include markdown code block formatting (no backticks like ```html).";

            $translated_html = call_llm($html_prompt, $source_content);
            $translated_html = trim($translated_html);
            if (strpos($translated_html, '```') !== false) {
                $translated_html = preg_replace('/^```(?:html|xml|json)?|```$/m', '', $translated_html);
                $translated_html = trim($translated_html);
            }

            $target_content_file = null;
            if (!empty($source_tile['content_file'])) {
                $base_file = pathinfo($source_tile['content_file'], PATHINFO_FILENAME);
                $base_clean = preg_replace('/[_\-](de|en)$/i', '', $base_file);
                $target_content_file = "{$base_clean}_{$target_lang}.html";
                file_put_contents($contents_dir . $target_content_file, $translated_html);
            }

            $new_tags = $translated_meta['tags'] ?? $tags_arr;
            $pg_tags = '{' . implode(',', array_map('trim', $new_tags)) . '}';

            $doc_text = format_tile_document_text($name, $target_lang, $new_tags, $translated_meta['summary']);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
            }

            $check_stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :target_lang");
            $check_stmt->execute([':name' => $name, ':target_lang' => $target_lang]);
            $existing_target = $check_stmt->fetch();

            if ($existing_target) {
                $sql = "
                    UPDATE tiles 
                    SET tags = :tags,
                        title = :title,
                        html_teaser = :html_teaser,
                        summary = :summary,
                        link = :link,
                        type = :type,
                        content_file = :content_file,
                        visible = :visible,
                        accent_color = :accent_color,
                        background = :background,
                        embedding = :embedding::vector,
                        sort_order = :sort_order,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = :id
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':id', $existing_target['id'], PDO::PARAM_INT);
            } else {
                $sql = "
                    INSERT INTO tiles (
                        name, language, tags, title, html_teaser,
                        summary, link, type, content_file,
                        visible, accent_color, background, embedding, sort_order, created_at, updated_at
                    ) VALUES (
                        :name, :language, :tags, :title, :html_teaser,
                        :summary, :link, :type, :content_file,
                        :visible, :accent_color, :background, :embedding::vector, :sort_order,
                        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                    )
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':language', $target_lang, PDO::PARAM_STR);
            }

            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':tags', $pg_tags, PDO::PARAM_STR);
            $stmt->bindValue(':title', $translated_meta['title'], PDO::PARAM_STR);
            $stmt->bindValue(':html_teaser', $translated_html, PDO::PARAM_STR);
            $stmt->bindValue(':summary', $translated_meta['summary'], PDO::PARAM_STR);
            $stmt->bindValue(':link', $source_tile['link'], PDO::PARAM_STR);
            $stmt->bindValue(':type', $source_tile['type'], PDO::PARAM_STR);
            $stmt->bindValue(':content_file', $target_content_file, PDO::PARAM_STR);
            $stmt->bindValue(':visible', (bool)$source_tile['visible'], PDO::PARAM_BOOL);
            $stmt->bindValue(':accent_color', $source_tile['accent_color'], PDO::PARAM_STR);
            $stmt->bindValue(':background', $source_tile['background'], PDO::PARAM_STR);
            $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
            $stmt->bindValue(':sort_order', (int)$source_tile['sort_order'], PDO::PARAM_INT);
            $stmt->execute();

            echo json_encode([
                'status' => 'success',
                'message' => "Successfully translated tile '{$name}' from {$source_lang} to {$target_lang}."
            ]);
            break;

        case 'save_config':
            $config_json = $_POST['config_json'] ?? '';
            if (empty($config_json)) {
                throw new Exception("Configuration content cannot be empty.");
            }
            // Validate JSON
            $decoded = json_decode($config_json, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("Invalid JSON format: " . json_last_error_msg());
            }

            // Write back to config.json
            $config_path = __DIR__ . '/config.json';
            $success = file_put_contents($config_path, json_encode($decoded, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES));
            if ($success === false) {
                throw new Exception("Failed to write to config.json.");
            }

            echo json_encode([
                'status' => 'success',
                'message' => 'Configuration saved successfully.'
            ]);
            break;

        case 'login':
            $password = $_POST['password'] ?? '';
            if (password_verify($password, ADMIN_PASSWORD_HASH)) {
                $_SESSION['admin_logged_in'] = true;
                echo json_encode([
                    'status' => 'success',
                    'message' => 'Logged in successfully.'
                ]);
            } else {
                http_response_code(401);
                echo json_encode([
                    'status' => 'error',
                    'message' => 'Invalid password.'
                ]);
            }
            break;

        case 'logout':
            $_SESSION['admin_logged_in'] = false;
            session_destroy();
            echo json_encode([
                'status' => 'success',
                'message' => 'Logged out successfully.'
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
                    // Postgres arrays come back as '{tag1,tag2}' or similar
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
                        visible, accent_color, background, embedding, sort_order
                    ) VALUES (
                        :name, :language, :tags, :title, :html_teaser, 
                        :summary, :link, :type, :content_file, 
                        :visible, :accent_color, :background, :embedding::vector, :sort_order
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
            $stmt->bindValue(':accent_color', $accent_color, PDO::PARAM_STR);
            $stmt->bindValue(':background', $background ?: null, PDO::PARAM_STR);
            $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
            $stmt->bindValue(':sort_order', $sort_order, PDO::PARAM_INT);
            $stmt->execute();

            echo json_encode([
                'status' => 'success',
                'message' => 'Tile saved successfully.'
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
                    visible, accent_color, background, embedding, sort_order
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser, 
                    :summary, :link, :type, :content_file, 
                    :visible, :accent_color, :background, :embedding::vector, :sort_order
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

        case 'get_content_file':
            $file = $_GET['file'] ?? '';
            if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $file)) {
                throw new Exception("Invalid file name format.");
            }
            $path = __DIR__ . '/content/' . $file;
            if (!file_exists($path)) {
                echo json_encode(['status' => 'success', 'content' => '']);
            } else {
                echo json_encode(['status' => 'success', 'content' => file_get_contents($path)]);
            }
            break;

        case 'save_content_file':
            $file = $_POST['file'] ?? '';
            $content = $_POST['content'] ?? '';
            if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $file)) {
                throw new Exception("Invalid file name format.");
            }
            $path = __DIR__ . '/content/' . $file;
            if (file_put_contents($path, $content) === false) {
                throw new Exception("Failed to write to file.");
            }
            echo json_encode(['status' => 'success', 'message' => 'File saved successfully.']);
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

        case 'suggest_meta':
            $name = trim($_POST['name'] ?? '');
            $language = trim($_POST['language'] ?? 'de');
            $title = trim($_POST['title'] ?? '');
            $content_file = trim($_POST['content_file'] ?? '');
            $html_teaser = trim($_POST['html_teaser'] ?? '');

            if (empty($name) || empty($title)) {
                throw new Exception("Name and title are required for suggestions.");
            }

            $content = '';
            if (!empty($content_file)) {
                if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $content_file)) {
                    throw new Exception("Invalid content file format.");
                }
                $path = __DIR__ . '/content/' . $content_file;
                if (file_exists($path)) {
                    $content = file_get_contents($path);
                }
            }
            if (empty($content)) {
                $content = $html_teaser;
            }

            $plain_text = strip_tags($content);
            if (empty($plain_text)) {
                $plain_text = "No additional content provided.";
            }

            $system_prompt = "You are an AI assistant helping organize content tiles. You will be given a tile's name, title, and plain text content.
Analyze the content and generate:
1. A concise, high-level summary (Kurzfassung) of the tile, combining the title and main content, up to 400 words (500 tokens). This summary is strictly used for vector search.
2. 3 to 6 category tags representing the main themes of the tile.

Respond ONLY with a valid JSON object matching the following structure (no markdown formatting, no backticks, no extra text):
{
  \"summary\": \"The high-level summary here...\",
  \"tags\": [\"tag1\", \"tag2\", \"tag3\"]
}";

            $user_content = "Name: $name\nTitle: $title\nContent:\n$plain_text";

            $llm_response = call_llm($system_prompt, $user_content);
            $llm_response = trim($llm_response);
            if (strpos($llm_response, '```') !== false) {
                $llm_response = preg_replace('/^```(?:json)?|```$/m', '', $llm_response);
                $llm_response = trim($llm_response);
            }

            $parsed = json_decode($llm_response, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("LLM returned invalid JSON: " . $llm_response);
            }

            echo json_encode([
                'status' => 'success',
                'data' => $parsed
            ]);
            break;

        case 'edit_html_with_llm':
            $prompt = trim($_POST['prompt'] ?? '');
            $html_teaser = $_POST['html_teaser'] ?? '';

            if (empty($prompt)) {
                throw new Exception("Prompt instruction is required.");
            }

            $system_prompt = "You are an expert HTML editor assistant. Follow the user's instructions to modify or transform the provided HTML document. Respond ONLY with the modified HTML document. Do not include markdown code block formatting (no backticks like ```html), explanations, or any extra text.";
            $user_content = "Instruction: $prompt\n\nHTML Document:\n$html_teaser";

            $llm_response = call_llm($system_prompt, $user_content);
            $llm_response = trim($llm_response);
            if (strpos($llm_response, '```') !== false) {
                $llm_response = preg_replace('/^```(?:html|xml|json)?|```$/m', '', $llm_response);
                $llm_response = trim($llm_response);
            }

            echo json_encode([
                'status' => 'success',
                'html_teaser' => $llm_response
            ]);
            break;

        case 'list_images':
            $dir = __DIR__ . '/tileimg';
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            $files = glob($dir . '/*.{webp,WEBP,png,PNG,jpg,jpeg,JPG,JPEG,gif,GIF,svg,SVG}', GLOB_BRACE);
            $images = [];
            foreach ($files as $file) {
                $images[] = [
                    'name' => basename($file),
                    'size' => filesize($file),
                    'url' => './tileimg/' . basename($file)
                ];
            }
            usort($images, function($a, $b) {
                return strcasecmp($a['name'], $b['name']);
            });
            echo json_encode([
                'status' => 'success',
                'images' => $images
            ]);
            break;

        case 'upload_image':
            if (!isset($_FILES['image']) || $_FILES['image']['error'] !== UPLOAD_ERR_OK) {
                throw new Exception("No file uploaded or upload error.");
            }
            
            $tmpPath = $_FILES['image']['tmp_name'];
            $origName = $_FILES['image']['name'];
            $fileSize = $_FILES['image']['size'];
            
            $imgInfo = getimagesize($tmpPath);
            if ($imgInfo === false) {
                throw new Exception("Uploaded file is not a valid image.");
            }
            
            $dir = __DIR__ . '/tileimg';
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            
            $origExt = strtolower(pathinfo($origName, PATHINFO_EXTENSION));
            $baseName = pathinfo($origName, PATHINFO_FILENAME);
            $cleanName = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $baseName);
            
            // 1. If file is <= 50kb (51200 bytes), do not edit or convert it at all
            if ($fileSize <= 51200) {
                $finalName = $cleanName . '.' . $origExt;
                $counter = 1;
                while (file_exists($dir . '/' . $finalName)) {
                    $finalName = $cleanName . '_' . $counter . '.' . $origExt;
                    $counter++;
                }
                $targetPath = $dir . '/' . $finalName;
                if (!move_uploaded_file($tmpPath, $targetPath)) {
                    throw new Exception("Failed to move uploaded image.");
                }
            } else {
                // Otherwise, it gets converted to WebP
                $targetExt = 'webp';
                $finalName = $cleanName . '.' . $targetExt;
                $counter = 1;
                while (file_exists($dir . '/' . $finalName)) {
                    $finalName = $cleanName . '_' . $counter . '.' . $targetExt;
                    $counter++;
                }
                $targetPath = $dir . '/' . $finalName;
                
                // If WebP and <= 100kb, move it unmodified.
                if ($origExt === 'webp' && $fileSize <= 102400) {
                    if (!move_uploaded_file($tmpPath, $targetPath)) {
                        throw new Exception("Failed to move uploaded WebP image.");
                    }
                } else {
                    // Otherwise, use ImageMagick convert command (or GD fallback if needed)
                    $quality = ($fileSize > 102400) ? 40 : 85;
                    $resizeCmd = ($fileSize > 102400) ? '-resize "640x640>"' : '';
                    
                    $escapedTmp = escapeshellarg($tmpPath);
                    $escapedTarget = escapeshellarg($targetPath);
                    
                    $cmd = "/opt/homebrew/bin/convert $escapedTmp $resizeCmd -quality $quality $escapedTarget 2>&1";
                    exec($cmd, $shellOutput, $returnCode);
                    
                    if ($returnCode !== 0) {
                        error_log("ImageMagick failed (code $returnCode): " . implode("\n", $shellOutput) . ". Falling back to GD.");
                        
                        $srcImg = imagecreatefromstring(file_get_contents($tmpPath));
                        if ($srcImg === false) {
                            throw new Exception("Failed to load image for processing.");
                        }
                        
                        $w = imagesx($srcImg);
                        $h = imagesy($srcImg);
                        
                        if ($fileSize > 102400) {
                            $max_w = 640;
                            $max_h = 640;
                            if ($w > $max_w || $h > $max_h) {
                                if ($w > $h) {
                                    $h = floor($h * ($max_w / $w));
                                    $w = $max_w;
                                } else {
                                    $w = floor($w * ($max_h / $h));
                                    $h = $max_h;
                                }
                            }
                            $destImg = imagecreatetruecolor($w, $h);
                            imagealphablending($destImg, false);
                            imagesavealpha($destImg, true);
                            imagecopyresampled($destImg, $srcImg, 0, 0, 0, 0, $w, $h, imagesx($srcImg), imagesy($srcImg));
                            imagewebp($destImg, $targetPath, 40);
                            imagedestroy($destImg);
                        } else {
                            $destImg = imagecreatetruecolor($w, $h);
                            imagealphablending($destImg, false);
                            imagesavealpha($destImg, true);
                            imagecopyresampled($destImg, $srcImg, 0, 0, 0, 0, $w, $h, $w, $h);
                            imagewebp($destImg, $targetPath, 85);
                            imagedestroy($destImg);
                        }
                        imagedestroy($srcImg);
                    }
                }
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image uploaded successfully.',
                'image' => [
                    'name' => $finalName,
                    'url' => './tileimg/' . $finalName
                ]
            ]);
            break;

        case 'rename_image':
            $oldName = trim($_POST['old_name'] ?? '');
            $newName = trim($_POST['new_name'] ?? '');
            
            if (empty($oldName) || empty($newName)) {
                throw new Exception("Old and new filenames are required.");
            }
            
            $oldExt = strtolower(pathinfo($oldName, PATHINFO_EXTENSION));
            $newNameBase = pathinfo($newName, PATHINFO_FILENAME);
            $newNameClean = preg_replace('/[^a-zA-Z0-9_\-]/', '_', $newNameBase) . '.' . $oldExt;
            
            $oldPath = __DIR__ . '/tileimg/' . $oldName;
            $newPath = __DIR__ . '/tileimg/' . $newNameClean;
            
            if (!file_exists($oldPath)) {
                throw new Exception("Source file does not exist.");
            }
            if (file_exists($newPath)) {
                throw new Exception("A file with the target name already exists.");
            }
            
            $stmt = $db->prepare("SELECT COUNT(*) FROM tiles WHERE background LIKE :pattern");
            $stmt->execute([':pattern' => '%tileimg/' . $oldName . '%']);
            $count = (int)$stmt->fetchColumn();
            
            if ($count > 0) {
                throw new Exception("Renaming rejected: This image is currently in use as a background on $count tile(s).");
            }
            
            if (!rename($oldPath, $newPath)) {
                throw new Exception("Failed to rename file on disk.");
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image renamed successfully.',
                'new_name' => $newNameClean
            ]);
            break;

        case 'delete_image':
            $name = trim($_POST['name'] ?? '');
            if (empty($name)) {
                throw new Exception("Filename is required.");
            }
            
            $path = __DIR__ . '/tileimg/' . $name;
            if (!file_exists($path)) {
                throw new Exception("File does not exist.");
            }
            
            $stmt = $db->prepare("SELECT COUNT(*) FROM tiles WHERE background LIKE :pattern");
            $stmt->execute([':pattern' => '%tileimg/' . $name . '%']);
            $count = (int)$stmt->fetchColumn();
            
            if ($count > 0) {
                throw new Exception("Deletion rejected: This image is currently in use as a background on $count tile(s).");
            }
            
            if (!unlink($path)) {
                throw new Exception("Failed to delete file from disk.");
            }
            
            echo json_encode([
                'status' => 'success',
                'message' => 'Image deleted successfully.'
            ]);
            break;

        default:
            throw new Exception("Invalid administrative action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
