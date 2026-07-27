<?php
session_start();
header('Content-Type: application/json; charset=utf-8');

// Ensure generous time limit for LLM translations & fallbacks as requested
set_time_limit(300);
ini_set('max_execution_time', '300');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? $_POST['action'] ?? 'status';

// Auth check
$is_admin = !empty($_SESSION['admin_logged_in']);
if (!$is_admin && $action !== 'login_check') {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Admin login required.']);
    exit;
}

try {
    $db = get_db_connection();

    switch ($action) {
        case 'status':
            $tile_name = $_GET['name'] ?? $_POST['name'] ?? null;
            $matrix = get_translation_status($tile_name);
            echo json_encode([
                'status' => 'success',
                'data' => $matrix
            ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE);
            break;

        case 'translate':
            $name = trim($_POST['name'] ?? $_GET['name'] ?? '');
            $target_lang = strtolower(trim($_POST['target_lang'] ?? $_GET['target_lang'] ?? 'en'));
            $target_lang = in_array($target_lang, ['de', 'en']) ? $target_lang : 'en';

            if (empty($name)) {
                throw new Exception("Tile name is required for translation.");
            }

            // Fetch source card (oldest created_at for this name)
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

            // Read source content file if defined
            $contents_dir = __DIR__ . '/contents/';
            $source_content = '';
            if (!empty($source_tile['content_file'])) {
                $fpath = $contents_dir . $source_tile['content_file'];
                if (file_exists($fpath)) {
                    $source_content = file_get_contents($fpath);
                }
            }
            if (empty($source_content)) {
                $source_content = $source_tile['html_content'];
            }

            // Clean tags string to array
            $raw_tags = trim($source_tile['category_tags'], '{}');
            $tags_arr = array_filter(array_map('trim', explode(',', $raw_tags)));

            // 1. LLM Prompt to translate card metadata
            $lang_full_name = ($target_lang === 'en') ? 'English' : 'German';
            $meta_prompt = "You are an expert bilingual content translator. Translate the following tile metadata from " . strtoupper($source_lang) . " into {$lang_full_name}.
Maintain the same tone, professional vocabulary, and theme.
Respond ONLY with a valid JSON object matching this structure (no markdown formatting, no backticks, no extra text):
{
  \"title\": \"Translated Title\",
  \"high_level_summary\": \"Translated high-level summary (up to 400 words)...\",
  \"category_tags\": [\"tag1\", \"tag2\", \"tag3\"]
}";
            $meta_user = "Source Title: {$source_tile['title']}\nSource Tags: " . implode(', ', $tags_arr) . "\nSource Summary: {$source_tile['high_level_summary']}";

            $meta_response = call_llm($meta_prompt, $meta_user);
            $meta_response = trim($meta_response);
            if (strpos($meta_response, '```') !== false) {
                $meta_response = preg_replace('/^```(?:json)?|```$/m', '', $meta_response);
                $meta_response = trim($meta_response);
            }
            $translated_meta = json_decode($meta_response, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("LLM metadata translation returned invalid JSON: " . $meta_response);
            }

            // 2. LLM Prompt to translate HTML content
            $html_prompt = "You are an expert HTML translator. Translate all human-readable text in the provided HTML snippet into {$lang_full_name}.
Keep all HTML tags, structure, classes, IDs, icons (<i class=\"...\"></i>), and attributes intact.
Respond ONLY with the translated HTML string. Do not include markdown code block formatting (no backticks like ```html), explanations, or extra text.";

            $translated_html = call_llm($html_prompt, $source_content);
            $translated_html = trim($translated_html);
            if (strpos($translated_html, '```') !== false) {
                $translated_html = preg_replace('/^```(?:html|xml|json)?|```$/m', '', $translated_html);
                $translated_html = trim($translated_html);
            }

            // Determine target content file path if source had one
            $target_content_file = null;
            if (!empty($source_tile['content_file'])) {
                $base_file = pathinfo($source_tile['content_file'], PATHINFO_FILENAME);
                // Strip existing language suffix if present
                $base_clean = preg_replace('/[_\-](de|en)$/i', '', $base_file);
                $target_content_file = "{$base_clean}_{$target_lang}.html";
                file_put_contents($contents_dir . $target_content_file, $translated_html);
            }

            // Clean tags to PostgreSQL vector tag literal
            $new_tags = $translated_meta['category_tags'] ?? $tags_arr;
            $pg_tags = '{' . implode(',', array_map('trim', $new_tags)) . '}';

            // 3. Generate Vector Embedding for translated card
            $doc_text = format_tile_document_text($name, $target_lang, $new_tags, $translated_meta['high_level_summary']);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
                error_log("LLM Admin Translate embedding fallback: " . $e->getMessage());
            }

            // Check if target language row exists
            $check_stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :target_lang");
            $check_stmt->execute([':name' => $name, ':target_lang' => $target_lang]);
            $existing_target = $check_stmt->fetch();

            if ($existing_target) {
                // Update target row
                $sql = "
                    UPDATE tiles 
                    SET category_tags = :category_tags,
                        title = :title,
                        html_content = :html_content,
                        high_level_summary = :high_level_summary,
                        link = :link,
                        reference_type = :reference_type,
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
                // Insert new target language row
                $sql = "
                    INSERT INTO tiles (
                        name, language, category_tags, title, html_content,
                        high_level_summary, link, reference_type, content_file,
                        visible, accent_color, background, embedding, sort_order, created_at, updated_at
                    ) VALUES (
                        :name, :language, :category_tags, :title, :html_content,
                        :high_level_summary, :link, :reference_type, :content_file,
                        :visible, :accent_color, :background, :embedding::vector, :sort_order,
                        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                    )
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':language', $target_lang, PDO::PARAM_STR);
            }

            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':category_tags', $pg_tags, PDO::PARAM_STR);
            $stmt->bindValue(':title', $translated_meta['title'], PDO::PARAM_STR);
            $stmt->bindValue(':html_content', $translated_html, PDO::PARAM_STR);
            $stmt->bindValue(':high_level_summary', $translated_meta['high_level_summary'], PDO::PARAM_STR);
            $stmt->bindValue(':link', $source_tile['link'], PDO::PARAM_STR);
            $stmt->bindValue(':reference_type', $source_tile['reference_type'], PDO::PARAM_STR);
            $stmt->bindValue(':content_file', $target_content_file, PDO::PARAM_STR);
            $stmt->bindValue(':visible', (bool)$source_tile['visible'], PDO::PARAM_BOOL);
            $stmt->bindValue(':accent_color', $source_tile['accent_color'], PDO::PARAM_STR);
            $stmt->bindValue(':background', $source_tile['background'], PDO::PARAM_STR);
            $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
            $stmt->bindValue(':sort_order', (int)$source_tile['sort_order'], PDO::PARAM_INT);
            $stmt->execute();

            echo json_encode([
                'status' => 'success',
                'message' => "Successfully translated tile '{$name}' from {$source_lang} into {$target_lang}.",
                'data' => [
                    'name' => $name,
                    'source_language' => $source_lang,
                    'target_language' => $target_lang,
                    'title' => $translated_meta['title'],
                    'summary' => $translated_meta['high_level_summary'],
                    'tags' => $new_tags,
                    'content_file' => $target_content_file
                ]
            ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE);
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
