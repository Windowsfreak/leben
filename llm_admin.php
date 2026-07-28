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
        case 'create':
            $name = trim($_POST['name'] ?? $_GET['name'] ?? '');
            $language = strtolower(trim($_POST['language'] ?? $_GET['language'] ?? 'de'));
            $title = trim($_POST['title'] ?? $_GET['title'] ?? '');
            $summary = trim($_POST['summary'] ?? $_GET['summary'] ?? '');
            $html_teaser = trim($_POST['html_teaser'] ?? $_GET['html_teaser'] ?? '');
            $content_file = trim($_POST['content_file'] ?? $_GET['content_file'] ?? '');
            $link = trim($_POST['link'] ?? $_GET['link'] ?? '');
            $type = trim($_POST['type'] ?? $_GET['type'] ?? 'doc');
            $accent_color = trim($_POST['accent_color'] ?? $_GET['accent_color'] ?? '#fbbf24');
            $background = trim($_POST['background'] ?? $_GET['background'] ?? '');
            $visible = isset($_POST['visible']) ? ($_POST['visible'] === 'true' || $_POST['visible'] === true || $_POST['visible'] === '1') : true;
            $secret = trim($_POST['secret'] ?? $_GET['secret'] ?? '');
            $sort_order = isset($_POST['sort_order']) ? (int)$_POST['sort_order'] : 100;

            $raw_tags = $_POST['tags'] ?? $_GET['tags'] ?? '';
            if (is_array($raw_tags)) {
                $tags_arr = $raw_tags;
            } else {
                $tags_arr = array_filter(array_map('trim', explode(',', (string)$raw_tags)));
            }
            $pg_tags = '{' . implode(',', $tags_arr) . '}';

            if (empty($name) || empty($title)) {
                throw new Exception("Name and title are required to create a card.");
            }

            $check = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :language");
            $check->execute([':name' => $name, ':language' => $language]);
            if ($check->fetch()) {
                throw new Exception("Card '{$name}' with language '{$language}' already exists.");
            }

            $doc_text = format_tile_document_text($name, $language, $tags_arr, $summary);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
                error_log("LLM Admin Create embedding fallback: " . $e->getMessage());
            }

            if (!empty($content_file) && !empty($html_teaser)) {
                if (preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $content_file)) {
                    $contents_dir = __DIR__ . '/content/';
                    file_put_contents($contents_dir . $content_file, $html_teaser);
                }
            }

            $sql = "
                INSERT INTO tiles (
                    name, language, tags, title, html_teaser,
                    summary, link, type, content_file,
                    visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser,
                    :summary, :link, :type, :content_file,
                    :visible, :secret, :accent_color, :background, :embedding::vector, :sort_order,
                    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                ) RETURNING id
            ";
            $stmt = $db->prepare($sql);
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
            $new_id = $stmt->fetchColumn();

            echo json_encode([
                'status' => 'success',
                'message' => "Successfully created master tile '{$name}' ({$language}).",
                'data' => [
                    'id' => (int)$new_id,
                    'name' => $name,
                    'language' => $language,
                    'title' => $title,
                    'tags' => $tags_arr,
                    'summary' => $summary,
                    'content_file' => $content_file
                ]
            ], JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE);
            break;

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
            $target_lang = strtolower(trim($_POST['target_lang'] ?? $_GET['target_lang'] ?? 'all'));

            if (empty($name)) {
                throw new Exception("Tile name is required for translation.");
            }

            $res = execute_auto_translate($name, $target_lang);

            echo json_encode([
                'status' => 'success',
                'message' => "Translation completed for '{$name}'.",
                'data' => $res
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
